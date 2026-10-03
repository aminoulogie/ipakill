/* The program's entry point, as bionic's crtbegin_dynamic.o has it (Android 4.2, x86):
   hand the kernel's argument block, main and the (empty) constructor lists to __libc_init. */
	.text
	.globl _start
_start:
	mov  %esp, %eax
	and  $-16, %esp
	sub  $12, %esp          /* keep the stack 16-byte aligned at the call */
	push $structors
	push $main
	push $0                 /* onexit */
	push %eax               /* raw_args: argc, argv..., envp..., auxv */
	call __libc_init
	hlt

	.data
	.align 4
structors:                 /* preinit, init, fini arrays: each is {-1, NULL} */
	.long empty, empty, empty
empty:
	.long -1, 0
