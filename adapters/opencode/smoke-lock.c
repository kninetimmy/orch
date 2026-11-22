//go:build ignore

/* Smoke-only Node-API bridge. The Node mutator, not a child, owns the handle. */
#ifdef __APPLE__
#define _DARWIN_C_SOURCE
#elif !defined(_WIN32)
#define _DEFAULT_SOURCE
#endif
#include <stddef.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#ifdef _WIN32
#include <windows.h>
#define EXPORT __declspec(dllexport)
#define CALL __cdecl
typedef HANDLE smoke_handle;
typedef uint16_t path_char;
#define PATH_GET napi_get_value_string_utf16
#define close_handle CloseHandle
#else
#include <errno.h>
#include <fcntl.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <unistd.h>
#define EXPORT __attribute__((visibility("default")))
#define CALL
typedef int smoke_handle;
typedef char path_char;
#define PATH_GET napi_get_value_string_utf8
#define close_handle close
#endif

/* Stable public Node-API v1 signatures used here (no V8, CRT fd table or
 * Node SDK/library lookup). Finalizers also belong to the loading Node isolate. */
typedef struct napi_env__ *napi_env;
typedef struct napi_value__ *napi_value;
typedef struct napi_callback_info__ *napi_callback_info;
typedef napi_value (CALL *napi_callback)(napi_env, napi_callback_info);
typedef void (CALL *napi_finalize)(napi_env, void *, void *);
#define API(X) \
    X(napi_get_cb_info, (napi_env, napi_callback_info, size_t *, napi_value *, napi_value *, void **)) \
    X(PATH_GET, (napi_env, napi_value, path_char *, size_t, size_t *)) \
    X(napi_get_null, (napi_env, napi_value *)) \
    X(napi_get_boolean, (napi_env, bool, napi_value *)) \
    X(napi_create_string_utf8, (napi_env, const char *, size_t, napi_value *)) \
    X(napi_create_string_utf16, (napi_env, const uint16_t *, size_t, napi_value *)) \
    X(napi_call_function, (napi_env, napi_value, napi_value, size_t, const napi_value *, napi_value *)) \
    X(napi_set_named_property, (napi_env, napi_value, const char *, napi_value)) \
    X(napi_create_function, (napi_env, const char *, size_t, napi_callback, void *, napi_value *)) \
    X(napi_wrap, (napi_env, napi_value, void *, napi_finalize, void *, void *)) \
    X(napi_throw_error, (napi_env, const char *, const char *))

#ifdef _WIN32
#define DECLARE(name, args) typedef int (CALL *name##_fn) args; static name##_fn name;
#define EXPAND(name, args) DECLARE(name, args)
API(EXPAND)
#else
#define DECLARE(name, args) extern int name args;
API(DECLARE)
#endif

#ifdef __linux__
#include <sys/vfs.h>

static bool initial_namespace(const char *path, unsigned long expected) {
    int fd = open(path, O_RDONLY | O_CLOEXEC);
    if (fd < 0) return false;
    struct stat info;
    struct statfs fs;
    bool initial = fstat(fd, &info) == 0 && fstatfs(fd, &fs) == 0 &&
        fs.f_type == 0x6e736673 && info.st_ino == expected; // NSFS_MAGIC
    close(fd);
    return initial;
}

static napi_value CALL linux_process_scope(napi_env env, napi_callback_info info) {
    (void)info;
    // The kernel's fixed initial namespace inodes (linux/nsfs.h) are
    // different from namespace-local PID 1 or equal container scopes.
    bool visible = initial_namespace("/proc/self/ns/pid", 0xeffffffc) &&
        initial_namespace("/proc/self/ns/user", 0xeffffffd);
    struct statfs fs;
    visible = visible && statfs("/proc", &fs) == 0 && fs.f_type == 0x9fa0; // PROC_SUPER_MAGIC
    napi_value result;
    return napi_get_boolean(env, visible, &result) ? NULL : result;
}
#elif defined(__APPLE__)
#include <libproc.h>
#include <string.h>
#include <sys/sysctl.h>
#include <sys/xattr.h>
#include <sys/proc.h>
#elif defined(_WIN32)
#include <shellapi.h>
#include <winternl.h>
#include <wtsapi32.h>

typedef LONG (WINAPI *query_process_fn)(HANDLE, ULONG, PVOID, ULONG, PULONG);

/* WTS supplies the invoking user's processes (unlike null CIM fields).
 * Query that user's native argv and image using limited process access. */
static napi_value CALL windows_process_paths(napi_env env, napi_callback_info info) {
    size_t argc = 1;
    napi_value callback;
    if (napi_get_cb_info(env, info, &argc, &callback, NULL, NULL) || argc != 1) return NULL;
    PWTS_PROCESS_INFOW rows;
    DWORD count;
    if (!WTSEnumerateProcessesW(WTS_CURRENT_SERVER_HANDLE, 0, 1, &rows, &count)) {
        napi_throw_error(env, NULL, "cannot enumerate Windows smoke processes");
        return NULL;
    }
    HANDLE token = NULL;
    union { TOKEN_USER token_user; BYTE bytes[256]; } user;
    DWORD size;
    bool ok = OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token) &&
        GetTokenInformation(token, TokenUser, &user, sizeof(user), &size);
    if (token) CloseHandle(token);
    query_process_fn query = (query_process_fn)(void (*)(void))
        GetProcAddress(GetModuleHandleW(L"ntdll.dll"), "NtQueryInformationProcess");
    wchar_t *image = malloc(32768 * sizeof(*image));
    UNICODE_STRING *command = malloc(65536 + sizeof(*command));
    bool self = false;
    ok = ok && query && image && command;
    for (DWORD i = 0; ok && i < count; i++) {
        WTS_PROCESS_INFOW *row = &rows[i];
        if (!row->pUserSid || !EqualSid(row->pUserSid, user.token_user.User.Sid)) continue;
        if (row->ProcessId == GetCurrentProcessId()) self = true;
        HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, row->ProcessId);
        if (!process) {
            if (GetLastError() == ERROR_INVALID_PARAMETER) continue; // already exited
            ok = false;
            break;
        }
        DWORD length = 32768, code;
        const ULONG capacity = 65536 + sizeof(*command);
        bool known = QueryFullProcessImageNameW(process, 0, image, &length) &&
            query(process, 60 /* ProcessCommandLineInformation */, command, capacity, NULL) == 0;
        uintptr_t offset = known ? (uintptr_t)command->Buffer - (uintptr_t)command : 0;
        known = known && offset >= sizeof(*command) && offset <= capacity &&
            command->Length > 0 && command->Length % sizeof(wchar_t) == 0 &&
            command->Length + sizeof(wchar_t) <= capacity - offset;
        if (!known) {
            ok = GetExitCodeProcess(process, &code) && code != STILL_ACTIVE;
            CloseHandle(process);
            continue;
        }
        CloseHandle(process);
        command->Buffer[command->Length / sizeof(wchar_t)] = L'\0';
        int args;
        wchar_t **argv = CommandLineToArgvW(command->Buffer, &args);
        ok = argv != NULL;
        for (int j = -1; ok && j < args; j++) {
            napi_value value, unused;
            const wchar_t *s = j < 0 ? image : argv[j];
            ok = napi_create_string_utf16(env, (const uint16_t *)s, wcslen(s), &value) == 0 &&
                napi_call_function(env, callback, callback, 1, &value, &unused) == 0;
        }
        if (argv) LocalFree(argv);
    }
    free(command);
    free(image);
    WTSFreeMemory(rows);
    if (!ok || !self) napi_throw_error(env, NULL, "incomplete Windows smoke process inventory");
    return NULL;
}
#endif

#ifdef __APPLE__
static struct kinfo_proc *mac_user_processes(size_t *count) {
    int mib[] = {CTL_KERN, KERN_PROC, KERN_PROC_UID, (int)geteuid()};
    size_t bytes;
    if (sysctl(mib, 4, NULL, &bytes, NULL, 0) || bytes == 0 || bytes > 16 * 1024 * 1024) return NULL;
    bytes += 256 * sizeof(struct kinfo_proc); // allow transient processes
    struct kinfo_proc *rows = malloc(bytes);
    if (!rows) return NULL;
    if (sysctl(mib, 4, rows, &bytes, NULL, 0) || bytes % sizeof(*rows)) {
        free(rows);
        return NULL;
    }
    *count = bytes / sizeof(*rows);
    return rows;
}

static bool mac_exited(pid_t pid) {
    int mib[] = {CTL_KERN, KERN_PROC, KERN_PROC_PID, pid};
    struct kinfo_proc row;
    size_t size = sizeof(row);
    return sysctl(mib, 4, &row, &size, NULL, 0) == 0 &&
        (!size || (size == sizeof(row) && row.kp_proc.p_stat == SZOMB));
}

static bool visit_mac(napi_env env, napi_value callback, const char *text, size_t len) {
    napi_value value, unused;
    return napi_create_string_utf8(env, text, len, &value) == 0 &&
        napi_call_function(env, callback, callback, 1, &value, &unused) == 0;
}

static napi_value CALL mac_process_paths(napi_env env, napi_callback_info info) {
    size_t argc = 1, count = 0;
    napi_value callback;
    if (napi_get_cb_info(env, info, &argc, &callback, NULL, NULL) || argc != 1) return NULL;
    struct kinfo_proc *rows = mac_user_processes(&count);
    int argmax = 0;
    size_t length = sizeof(argmax);
    int argmib[] = {CTL_KERN, KERN_ARGMAX};
    bool ok = rows && !sysctl(argmib, 2, &argmax, &length, NULL, 0) && argmax > 0 && argmax <= 16 * 1024 * 1024;
    char *argv = ok ? malloc((size_t)argmax) : NULL;
    bool self = false;
    ok = argv != NULL;
    for (size_t i = 0; ok && i < count; i++) {
        pid_t pid = rows[i].kp_proc.p_pid;
        if (pid == getpid()) self = true;
        if (pid <= 0 || rows[i].kp_proc.p_stat == SZOMB) continue;
        char image[PROC_PIDPATHINFO_MAXSIZE];
        int mib[] = {CTL_KERN, KERN_PROCARGS2, pid};
        size_t bytes = argmax;
        if (proc_pidpath(pid, image, sizeof(image)) <= 0 ||
            sysctl(mib, 3, argv, &bytes, NULL, 0) || bytes <= sizeof(int)) {
            ok = mac_exited(pid);
            continue;
        }
        int args;
        memcpy(&args, argv, sizeof(args));
        const char *end = argv + bytes;
        const char *at = argv + sizeof(args);
        const char *zero = memchr(at, 0, (size_t)(end - at)); // exec path, then alignment padding
        if (!zero || args < 1) { ok = false; break; }
        at = zero + 1;
        while (at < end && !*at) at++;
        ok = visit_mac(env, callback, image, strlen(image));
        for (int j = 0; ok && j < args; j++) {
            zero = memchr(at, 0, (size_t)(end - at));
            ok = zero && visit_mac(env, callback, at, (size_t)(zero - at));
            if (zero) at = zero + 1;
        }
    }
    free(argv);
    free(rows);
    if (!ok || !self) napi_throw_error(env, NULL, "incomplete macOS smoke process inventory");
    return NULL;
}

static napi_value CALL has_resource_fork(napi_env env, napi_callback_info info) {
    size_t argc = 1, length;
    napi_value arg, result;
    char path[4096];
    if (napi_get_cb_info(env, info, &argc, &arg, NULL, NULL) || argc != 1 ||
        PATH_GET(env, arg, NULL, 0, &length) || length == 0 || length >= sizeof(path) ||
        PATH_GET(env, arg, path, sizeof(path), NULL)) {
        napi_throw_error(env, NULL, "invalid macOS resource fork path");
        return NULL;
    }
    ssize_t size = getxattr(path, "com.apple.ResourceFork", NULL, 0, 0, XATTR_NOFOLLOW);
    // ENOATTR alone means the fork is absent. A zero-length fork still exists.
    if (size < 0 && errno != ENOATTR) {
        napi_throw_error(env, NULL, "cannot inspect macOS resource fork");
        return NULL;
    }
    return napi_get_boolean(env, size >= 0, &result) ? NULL : result;
}
#endif

/* 0 held, 1 contended, -1 error. Never create, truncate or unlock by name. */
static int take_lock(const path_char *path, smoke_handle *held, unsigned long *error) {
#ifdef _WIN32
    HANDLE file = CreateFileW(path, GENERIC_READ | GENERIC_WRITE,
        FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_EXISTING, FILE_FLAG_OPEN_REPARSE_POINT, NULL);
    if (file == INVALID_HANDLE_VALUE) {
        *error = GetLastError();
        return -1;
    }
    BY_HANDLE_FILE_INFORMATION info;
    OVERLAPPED offset = {0};
    if (!GetFileInformationByHandle(file, &info)) {
        *error = GetLastError();
    } else if ((info.dwFileAttributes & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY)) ||
               info.nNumberOfLinks != 1 || info.nFileSizeHigh || info.nFileSizeLow ||
               GetFileType(file) != FILE_TYPE_DISK) {
        *error = ERROR_INVALID_DATA;
    } else if (LockFileEx(file, LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset)) {
        // No delete sharing: the checked path cannot be renamed while held.
        *held = file;
        return 0;
    } else {
        *error = GetLastError();
        CloseHandle(file);
        return *error == ERROR_LOCK_VIOLATION ? 1 : -1;
    }
    CloseHandle(file);
#else
    int file = open(path, O_RDWR | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK);
    if (file < 0) {
        *error = (unsigned long)errno;
        return -1;
    }
    struct stat info, named;
    if (fstat(file, &info) < 0) {
        *error = (unsigned long)errno;
    } else if (!S_ISREG(info.st_mode) || info.st_nlink != 1 || info.st_size ||
               info.st_uid != geteuid() || (info.st_mode & 077)) {
        *error = EINVAL;
    } else if (flock(file, LOCK_EX | LOCK_NB) < 0) {
        *error = (unsigned long)errno;
        close(file);
        return *error == EWOULDBLOCK ? 1 : -1;
    } else if (lstat(path, &named) < 0) {
        *error = (unsigned long)errno;
    } else if (info.st_dev != named.st_dev || info.st_ino != named.st_ino) {
        *error = EINVAL;
    } else {
        *held = file;
        return 0;
    }
    close(file);
#endif
    return -1;
}

typedef struct {
    smoke_handle file;
    int closed;
} lock_state;

static void release(lock_state *state) {
    if (!state->closed) {
        state->closed = 1;
        close_handle(state->file);
    }
}

static void CALL finalize(napi_env env, void *data, void *hint) {
    (void)env;
    (void)hint;
    release(data);
    free(data);
}

static napi_value CALL close_lock(napi_env env, napi_callback_info info) {
    void *data;
    if (napi_get_cb_info(env, info, NULL, NULL, NULL, &data) == 0) release(data);
    return NULL;
}

static napi_value CALL acquire(napi_env env, napi_callback_info info) {
    size_t argc = 1, length;
    /* Never allow a NUL-truncated pathname to name a different lock. */
    path_char path[32768];
    napi_value arg, result;
    if (napi_get_cb_info(env, info, &argc, &arg, NULL, NULL) || argc != 1 ||
        PATH_GET(env, arg, NULL, 0, &length) || length == 0 || length >= 32768 ||
        PATH_GET(env, arg, path, 32768, NULL)) {
        napi_throw_error(env, NULL, "invalid smoke lock path");
        return NULL;
    }
    for (size_t i = 0; i < length; i++) {
        if (path[i]) continue;
        napi_throw_error(env, NULL, "NUL in smoke lock path");
        return NULL;
    }
    smoke_handle file;
    unsigned long error;
    int status = take_lock(path, &file, &error);
    if (status == 1 && napi_get_null(env, &result) == 0) return result;
    if (status != 0) {
        char message[96];
        snprintf(message, sizeof(message), "cannot acquire native smoke file lock (OS error %lu)", error);
        napi_throw_error(env, NULL, message);
        return NULL;
    }
    lock_state *state = malloc(sizeof(*state));
    if (state) {
        *state = (lock_state){file, 0};
        if (napi_create_function(env, "closeSmokeLock", 14, close_lock, state, &result) == 0 &&
            napi_wrap(env, result, state, finalize, NULL, NULL) == 0) return result;
        free(state);
    }
    close_handle(file);
    napi_throw_error(env, NULL, "cannot retain native smoke lock handle");
    return NULL;
}

EXPORT napi_value CALL napi_register_module_v1(napi_env env, napi_value exports) {
#ifdef _WIN32
    HMODULE node = GetModuleHandleW(NULL);
#define RESOLVE(name, args) \
    name = (name##_fn)(void (*)(void))GetProcAddress(node, #name); \
    if (!name) return NULL;
#define RESOLVE_EXPANDED(name, args) RESOLVE(name, args)
    API(RESOLVE_EXPANDED)
#endif
    napi_value function;
    if (napi_create_function(env, "trySmokeLock", 12, acquire, NULL, &function) ||
        napi_set_named_property(env, exports, "trySmokeLock", function)) return NULL;
#ifdef __APPLE__
    if (napi_create_function(env, "hasResourceFork", 15, has_resource_fork, NULL, &function) ||
        napi_set_named_property(env, exports, "hasResourceFork", function)) return NULL;
    if (napi_create_function(env, "macProcessPaths", 15, mac_process_paths, NULL, &function) ||
        napi_set_named_property(env, exports, "macProcessPaths", function)) return NULL;
#elif defined(__linux__)
    if (napi_create_function(env, "linuxProcessScope", 17, linux_process_scope, NULL, &function) ||
        napi_set_named_property(env, exports, "linuxProcessScope", function)) return NULL;
#elif defined(_WIN32)
    if (napi_create_function(env, "windowsProcessPaths", 19, windows_process_paths, NULL, &function) ||
        napi_set_named_property(env, exports, "windowsProcessPaths", function)) return NULL;
#endif
    return exports;
}
