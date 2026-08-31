/*
 * sched-helper: Apply SCHED_DEADLINE to a PID.
 *
 * Compiled with -nostdlib to avoid glibc dependency. Uses raw syscalls
 * so the binary runs on any Linux kernel without glibc version issues.
 *
 * Usage: sched-helper <pid> <core> <runtime_ns> <period_ns>
 */

#define SYS_read             0
#define SYS_write            1
#define SYS_open             2
#define SYS_close            3
#define SYS_getpid          39
#define SYS_sched_setaffinity 203
#define SYS_sched_setattr   314
#define SYS_exit_group      231

#define O_WRONLY             1
#define O_RDONLY             0

#define SCHED_DEADLINE       6

typedef unsigned long uint64_t;
typedef unsigned int  uint32_t;
typedef int           int32_t;
typedef unsigned long size_t;

struct sched_attr_dl {
    uint32_t size;
    uint32_t sched_policy;
    uint64_t sched_flags;
    int32_t  sched_nice;
    uint32_t sched_priority;
    uint64_t sched_runtime;
    uint64_t sched_deadline;
    uint64_t sched_period;
    uint32_t sched_util_min;
    uint32_t sched_util_max;
};

static long syscall1(long nr, long a1) {
    long ret;
    __asm__ volatile("syscall" : "=a"(ret) : "a"(nr), "D"(a1) : "rcx", "r11", "memory");
    return ret;
}

static long syscall2(long nr, long a1, long a2) {
    long ret;
    __asm__ volatile("syscall" : "=a"(ret) : "a"(nr), "D"(a1), "S"(a2) : "rcx", "r11", "memory");
    return ret;
}

static long syscall3(long nr, long a1, long a2, long a3) {
    long ret;
    register long r10 __asm__("r10") = a3;
    (void)r10;
    __asm__ volatile("syscall" : "=a"(ret) : "a"(nr), "D"(a1), "S"(a2), "d"(a3) : "rcx", "r11", "memory");
    return ret;
}

static size_t str_len(const char *s) {
    size_t n = 0;
    while (s[n]) n++;
    return n;
}

static void write_str(int fd, const char *s) {
    syscall3(SYS_write, fd, (long)s, str_len(s));
}

static void write_int(int fd, long v) {
    char buf[32];
    int i = 30;
    int neg = 0;
    if (v < 0) { neg = 1; v = -v; }
    if (v == 0) { buf[i--] = '0'; }
    while (v > 0) { buf[i--] = '0' + (v % 10); v /= 10; }
    if (neg) buf[i--] = '-';
    syscall3(SYS_write, fd, (long)&buf[i+1], 30 - i);
}

static long parse_long(const char *s) {
    long v = 0;
    while (*s >= '0' && *s <= '9') {
        v = v * 10 + (*s - '0');
        s++;
    }
    return v;
}

static int write_pid_to_cgroup(const char *path, int pid) {
    long fd = syscall2(SYS_open, (long)path, O_WRONLY);
    if (fd < 0) return -1;
    char buf[16];
    int i = 14;
    int p = pid;
    if (p == 0) { buf[i--] = '0'; }
    while (p > 0) { buf[i--] = '0' + (p % 10); p /= 10; }
    buf[15] = '\n';
    syscall3(SYS_write, fd, (long)&buf[i+1], 15 - i);
    syscall1(SYS_close, fd);
    return 0;
}

/* Read /proc/<pid>/cgroup and extract the path after "0::" */
static int read_cgroup(int pid, char *out, int outlen) {
    char path[64];
    int i = 0;
    char prefix[] = "/proc/";
    for (int j = 0; prefix[j]; j++) path[i++] = prefix[j];
    /* int to string for pid */
    char pbuf[16];
    int pi = 14;
    int p = pid;
    if (p == 0) { pbuf[pi--] = '0'; }
    while (p > 0) { pbuf[pi--] = '0' + (p % 10); p /= 10; }
    for (int j = pi + 1; j <= 14; j++) path[i++] = pbuf[j];
    char suffix[] = "/cgroup";
    for (int j = 0; suffix[j]; j++) path[i++] = suffix[j];
    path[i] = 0;

    long fd = syscall2(SYS_open, (long)path, O_RDONLY);
    if (fd < 0) return -1;
    char buf[256];
    long n = syscall3(SYS_read, fd, (long)buf, 255);
    syscall1(SYS_close, fd);
    if (n <= 0) return -1;
    buf[n] = 0;

    /* Find "::" and extract path */
    for (int j = 0; j < n - 1; j++) {
        if (buf[j] == ':' && buf[j+1] == ':') {
            int k = 0;
            for (int l = j + 2; l < n && buf[l] != '\n' && k < outlen - 1; l++)
                out[k++] = buf[l];
            out[k] = 0;
            return 0;
        }
    }
    return -1;
}

static void restore_cgroup(int pid, const char *cgpath) {
    if (cgpath[0] == '/' && cgpath[1] == 0) return; /* root cgroup, nothing to restore */
    if (cgpath[0] == 0) return;
    char path[512];
    int i = 0;
    char prefix[] = "/sys/fs/cgroup";
    for (int j = 0; prefix[j]; j++) path[i++] = prefix[j];
    for (int j = 0; cgpath[j]; j++) path[i++] = cgpath[j];
    char suffix[] = "/cgroup.procs";
    for (int j = 0; suffix[j]; j++) path[i++] = suffix[j];
    path[i] = 0;
    write_pid_to_cgroup(path, pid);
}

static void real_start(long *sp);

__attribute__((naked)) void _start(void) {
    __asm__ volatile(
        "mov %%rsp, %%rdi\n"  /* pass stack pointer as first arg */
        "call real_start\n"
        : : : "memory"
    );
}

static void real_start(long *sp) {
    int argc = (int)sp[0];
    char **argv = (char **)(sp + 1);

    if (argc != 5) {
        write_str(2, "Usage: sched-helper <pid> <core> <runtime_ns> <period_ns>\n");
        syscall1(SYS_exit_group, 1);
    }

    int pid = (int)parse_long(argv[1]);
    int core = (int)parse_long(argv[2]);
    uint64_t runtime_ns = parse_long(argv[3]);
    uint64_t period_ns = parse_long(argv[4]);

    int self_pid = syscall1(SYS_getpid, 0);

    /* Save original cgroups */
    char self_cg[256] = {0}, target_cg[256] = {0};
    read_cgroup(self_pid, self_cg, 256);
    read_cgroup(pid, target_cg, 256);

    /* Move both to root cgroup */
    if (write_pid_to_cgroup("/sys/fs/cgroup/cgroup.procs", self_pid) < 0) {
        write_str(2, "Failed to move self to root cgroup\n");
        syscall1(SYS_exit_group, 1);
    }
    if (write_pid_to_cgroup("/sys/fs/cgroup/cgroup.procs", pid) < 0) {
        write_str(2, "Failed to move target to root cgroup\n");
        restore_cgroup(self_pid, self_cg);
        syscall1(SYS_exit_group, 1);
    }

    /* Set CPU affinity */
    unsigned long mask = 1UL << core;
    long ret = syscall3(SYS_sched_setaffinity, pid, 8, (long)&mask);
    if (ret < 0) {
        write_str(2, "sched_setaffinity failed\n");
        restore_cgroup(pid, target_cg);
        restore_cgroup(self_pid, self_cg);
        syscall1(SYS_exit_group, 1);
    }

    /* Set SCHED_DEADLINE */
    struct sched_attr_dl attr = {0};
    attr.size = sizeof(attr);
    attr.sched_policy = SCHED_DEADLINE;
    attr.sched_runtime = runtime_ns;
    attr.sched_deadline = period_ns;
    attr.sched_period = period_ns;

    ret = syscall3(SYS_sched_setattr, pid, (long)&attr, 0);
    if (ret < 0) {
        write_str(2, "sched_setattr failed (errno=");
        write_int(2, -ret);
        write_str(2, ")\n");
        restore_cgroup(pid, target_cg);
        restore_cgroup(self_pid, self_cg);
        syscall1(SYS_exit_group, 1);
    }

    /* Restore cgroups */
    restore_cgroup(pid, target_cg);
    restore_cgroup(self_pid, self_cg);

    write_str(2, "Applied SCHED_DEADLINE to pid ");
    write_int(2, pid);
    write_str(2, "\n");
    syscall1(SYS_exit_group, 0);
}
