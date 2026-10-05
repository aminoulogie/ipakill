#!/bin/sh
# Builds gltest for Android 4.2 x86 (bionic) with plain clang + lld, no NDK: the program
# links against stub libc.so/libdl.so made here (just the symbol names); at run time
# Android's linker (/system/bin/linker) binds them to the real /system/lib libraries.
set -e
cd "$(dirname "$0")"
# -mstackrealign: Android 4.2's libc calls main with the stack aligned to 4 bytes only, but
# clang assumes 16 and uses aligned SSE moves (movaps) on it.
T="-target i686-linux-android17 -fno-pic -fno-stack-protector -O2 -mstackrealign"
mkdir -p stubs
cat > stubs/libc.c <<'S'
void __libc_init(){} int snprintf(){return 0;} long write(){return 0;} void *malloc(){return 0;}
int usleep(){return 0;} int open(){return 0;} int ioctl(){return 0;} int close(){return 0;}
int clock_gettime(){return 0;} void exit(){} long read(){return 0;} void *mmap(){return 0;}
void *memset(){return 0;} void *calloc(){return 0;}
S
echo 'void *dlopen(){return 0;} void *dlsym(){return 0;} const char *dlerror(){return 0;}' > stubs/libdl.c
for l in libc libdl; do
  clang $T -shared -nostdlib -fPIC -Wl,-soname,$l.so -o stubs/$l.so stubs/$l.c
done
clang $T -c start.S -o start.o
for p in gltest glanim; do
  clang $T -Wall -c $p.c -o $p.o
  ld.lld -m elf_i386 -o $p --dynamic-linker=/system/bin/linker --hash-style=sysv \
    -z norelro --no-rosegment start.o $p.o stubs/libc.so stubs/libdl.so
  echo built $p
done

# glsf draws through SurfaceFlinger, so it calls the C++ libraries (libgui, libutils,
# libbinder). Compiled with clang++ but freestanding: no exceptions, no RTTI, no libstdc++
# (the only class, sp, has a trivial destructor and needs no C++ runtime). Linked the same
# way as gltest; at run time /system/bin/linker binds the stub symbols to the real libraries.
clang++ $T -Wall -fno-exceptions -fno-rtti -nostdlib++ -fno-threadsafe-statics -c glsf.cpp -o glsf.o
ld.lld -m elf_i386 -o glsf --dynamic-linker=/system/bin/linker --hash-style=sysv \
  -z norelro --no-rosegment start.o glsf.o stubs/libc.so stubs/libdl.so
echo built glsf

# condor-init carries glanim inside itself (go:embed) and starts it when animations are on.
cp glanim ../condor-init/glanim.bin
