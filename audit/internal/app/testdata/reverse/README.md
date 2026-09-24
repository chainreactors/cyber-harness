# Benign reverse-analysis fixture

`sample.pe` is a Windows x86-64 executable built from `sample.c` with MinGW GCC.
The release integration test analyzes it as data using radare2, capa and FLOSS;
it never executes it. The fixture prints a marker and compares an argument.

Rebuild with MinGW GCC (or `x86_64-w64-mingw32-gcc` on Linux):

```sh
gcc -Os -s -Wl,--no-insert-timestamp sample.c -o sample.pe
```

Committing the small PE keeps release tests independent of a C cross-compiler.
