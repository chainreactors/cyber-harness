#include <stdio.h>
#include <string.h>
__attribute__((noinline)) int audit_check(const char *s) { return strcmp(s,"AUDIT_REVERSE_FIXTURE") == 0 ? 7 : 2; }
int main(int argc, char **argv) { puts("AUDIT_REVERSE_FIXTURE"); return argc > 1 ? audit_check(argv[1]) : 0; }
