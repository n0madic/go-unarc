/* Runs one CLS codec DLL (FreeArc CLS API 0.10) as a file-to-file decompressor, under Wine.
 * usage: clshost.exe <CLS-xxx.dll> <params> <packed file> <unpacked file>
 * The native unarc hands the chain stages it cannot run itself (srep, msc) to this program. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <windows.h>

#define CLS_INIT            1
#define CLS_DONE            2
#define CLS_DECOMPRESS      4
#define CLS_FULL_READ       4096
#define CLS_PARTIAL_READ    5120
#define CLS_FULL_WRITE      6144
#define CLS_PARTIAL_WRITE   7168
#define CLS_STREAMS_PER_OP  1024
#define CLS_MALLOC          1
#define CLS_FREE            2
#define CLS_GET_PARAMSTR    3
#define CLS_ERROR_NOT_IMPLEMENTED   (-2)
#define CLS_ERROR_NOT_ENOUGH_MEMORY (-3)
#define CLS_ERROR_READ              (-4)
#define CLS_ERROR_WRITE             (-5)
#define IO_BUFFER_SIZE      (8 << 20)

typedef int (__cdecl CLS_CALLBACK)(void *instance, int op, void *ptr, int n);
typedef int (__cdecl *CLS_MAIN)(int op, CLS_CALLBACK *callback, void *instance);

struct host {
    FILE *in, *out;
    const char *params;
};

static int in_range(int op, int base) { return op >= base && op < base + CLS_STREAMS_PER_OP; }

static int __cdecl callback(void *instance, int op, void *ptr, int n)
{
    struct host *h = instance;
    if (in_range(op, CLS_FULL_READ) || in_range(op, CLS_PARTIAL_READ)) {
        if (op != CLS_FULL_READ && op != CLS_PARTIAL_READ) return 0;   /* only stream 0 exists */
        size_t got = fread(ptr, 1, (size_t)n, h->in);
        return ferror(h->in) ? CLS_ERROR_READ : (int)got;
    }
    if (in_range(op, CLS_FULL_WRITE) || in_range(op, CLS_PARTIAL_WRITE)) {
        if (op != CLS_FULL_WRITE && op != CLS_PARTIAL_WRITE) return CLS_ERROR_WRITE;
        return fwrite(ptr, 1, (size_t)n, h->out) == (size_t)n ? n : CLS_ERROR_WRITE;
    }
    switch (op) {
    case CLS_MALLOC:
        *(void **)ptr = malloc((size_t)n);
        return *(void **)ptr ? 0 : CLS_ERROR_NOT_ENOUGH_MEMORY;
    case CLS_FREE:
        free(ptr);
        return 0;
    case CLS_GET_PARAMSTR:
        if (n <= 0) n = 256;   /* the API caps the parameter string at 256 bytes */
        strncpy(ptr, h->params, (size_t)n - 1);
        ((char *)ptr)[n - 1] = 0;
        return 0;
    default:
        return CLS_ERROR_NOT_IMPLEMENTED;
    }
}

int main(int argc, char **argv)
{
    if (argc != 5) {
        fprintf(stderr, "usage: clshost <CLS-xxx.dll> <params> <packed> <unpacked>\n");
        return 2;
    }
    HMODULE dll = LoadLibraryA(argv[1]);
    if (!dll) { fprintf(stderr, "clshost: cannot load %s (%lu)\n", argv[1], GetLastError()); return 3; }
    CLS_MAIN cls_main = (CLS_MAIN)GetProcAddress(dll, "ClsMain");
    if (!cls_main) { fprintf(stderr, "clshost: %s has no ClsMain\n", argv[1]); return 3; }

    struct host h = { fopen(argv[3], "rb"), fopen(argv[4], "wb"), argv[2] };
    if (!h.in || !h.out) { fprintf(stderr, "clshost: cannot open %s or %s\n", argv[3], argv[4]); return 4; }
    setvbuf(h.in, NULL, _IOFBF, IO_BUFFER_SIZE);
    setvbuf(h.out, NULL, _IOFBF, IO_BUFFER_SIZE);

    cls_main(CLS_INIT, callback, &h);
    int ret = cls_main(CLS_DECOMPRESS, callback, &h);
    cls_main(CLS_DONE, callback, &h);

    if (fclose(h.out) != 0 && ret >= 0) ret = CLS_ERROR_WRITE;
    fclose(h.in);
    if (ret < 0) { fprintf(stderr, "clshost: %s failed with %d\n", argv[1], ret); return 1; }
    return 0;
}
