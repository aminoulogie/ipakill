#!/bin/sh
# Builds gltest for Android 4.2 x86 (bionic) with plain clang + lld, no NDK: the program
# links against stub libc.so/libdl.so made here (just the symbol names); at run time
# Android's linker (/system/bin/linker) binds them to the real /system/lib libraries.
set -e
cd "$(dirname "$0")"
T="-target i686-linux-android17 -fno-pic -fno-stack-protector -O2"
mkdir -p stubs
cat > stubs/libc.c <<'S'
void __libc_init(){} int snprintf(){return 0;} long write(){return 0;} void *malloc(){return 0;}
int usleep(){return 0;} int open(){return 0;} int ioctl(){return 0;} int close(){return 0;}
int clock_gettime(){return 0;} void exit(){} long read(){return 0;}
S
echo 'void *dlopen(){return 0;} void *dlsym(){return 0;} const char *dlerror(){return 0;}' > stubs/libdl.c
for l in libc libdl; do
  clang $T -shared -nostdlib -fPIC -Wl,-soname,$l.so -o stubs/$l.so stubs/$l.c
done
clang $T -c gltest.c -o gltest.o
clang $T -c start.S -o start.o
ld.lld -m elf_i386 -o gltest --dynamic-linker=/system/bin/linker --hash-style=sysv \
  -z norelro --no-rosegment start.o gltest.o stubs/libc.so stubs/libdl.so
echo built gltest
