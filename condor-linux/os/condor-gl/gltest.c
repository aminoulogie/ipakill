/*
 * gltest: can condor use the tablet's GPU (PowerVR SGX544) without Android's UI running?
 *
 * Built for Android itself (bionic, x86, API 17) with plain clang and no NDK (see build.sh):
 * it links only against libc and libdl, and dlopens Android's own libui, libEGL and
 * libGLESv2 from /system/lib at run time, the libraries Android's GPU-drawn screens use.
 *
 * It opens the framebuffer through Android's FramebufferNativeWindow
 * (android_createDisplaySurface, the way SurfaceFlinger did before hardware composers, so
 * SurfaceFlinger must be stopped), draws with OpenGL ES 2, and measures:
 *   1. frames per second clearing the screen (the swap rate, i.e. vsync),
 *   2. how long a full-screen 1200x1920 image takes to upload as a texture,
 *   3. frames per second sliding that image across the screen (a real animation).
 * At the end it puts the framebuffer's display offset back to 0, where condor-init draws.
 */
typedef unsigned int u32;
typedef int EGLint;
typedef void *EGLDisplay, *EGLConfig, *EGLSurface, *EGLContext, *EGLNativeWindowType;

/* libc / libdl (bionic) */
extern int snprintf(char *, unsigned, const char *, ...);
extern long write(int, const void *, unsigned);
extern void *malloc(unsigned);
extern int usleep(unsigned);
extern int open(const char *, int, ...);
extern int ioctl(int, int, ...);
extern int close(int);
struct timespec { long tv_sec, tv_nsec; };
extern int clock_gettime(int, struct timespec *);
extern void *dlopen(const char *, int);
extern void *dlsym(void *, const char *);
extern const char *dlerror(void);
extern void exit(int);
extern long read(int, void *, unsigned);

static char out[512];
#define say(...) do { int n = snprintf(out, sizeof out, __VA_ARGS__); write(1, out, n); } while (0)

static double now(void) {
	struct timespec t;
	clock_gettime(1 /* CLOCK_MONOTONIC */, &t);
	return t.tv_sec + t.tv_nsec / 1e9;
}

static void *lib(const char *name) {
	void *h = dlopen(name, 0 /* RTLD_LAZY on bionic */);
	if (!h) { say("FAIL: dlopen %s: %s\n", name, dlerror()); exit(2); }
	return h;
}
static void *sym(void *h, const char *name) {
	void *p = dlsym(h, name);
	if (!p) { say("FAIL: no %s\n", name); exit(2); }
	return p;
}

/* Put the framebuffer's pan offset back to 0 (struct fb_var_screeninfo: yoffset is u32 #5). */
static void unpan(void) {
	u32 v[40];
	int fd = open("/dev/graphics/fb0", 2);
	if (fd < 0) fd = open("/dev/fb0", 2);
	if (fd < 0) return;
	if (ioctl(fd, 0x4600 /* FBIOGET_VSCREENINFO */, v) == 0 && (v[4] || v[5])) {
		v[4] = v[5] = 0;
		ioctl(fd, 0x4606 /* FBIOPAN_DISPLAY */, v);
		say("framebuffer pan offset put back to 0\n");
	}
	close(fd);
}

/* diagnose explains why Android's EGL loader couldn't start the GPU driver. */
static void diagnose(void) {
	char buf[512];
	const char *cfgs[] = {"/vendor/lib/egl/egl.cfg", "/system/lib/egl/egl.cfg", 0};
	for (int i = 0; cfgs[i]; i++) {
		int fd = open(cfgs[i], 0);
		if (fd < 0) { say("  %s: missing\n", cfgs[i]); continue; }
		long n = read(fd, buf, sizeof buf - 1);
		close(fd);
		buf[n > 0 ? n : 0] = 0;
		say("  %s:\n%s\n", cfgs[i], buf);
	}
	const char *dirs[] = {"/vendor/lib/egl/", "/system/lib/egl/", "/system/vendor/lib/egl/", 0};
	const char *names[] = {"libEGL_POWERVR_SGX544_115.so", "libGLESv2_POWERVR_SGX544_115.so", 0};
	for (int d = 0; dirs[d]; d++)
		for (int k = 0; names[k]; k++) {
			char path[200];
			snprintf(path, sizeof path, "%s%s", dirs[d], names[k]);
			int fd = open(path, 0);
			if (fd < 0) continue;
			close(fd);
			void *h = dlopen(path, 0);
			say("  dlopen %s: %s\n", path, h ? "ok" : dlerror());
		}
	const char *devs[] = {"/dev/pvrsrvkm", "/dev/graphics/fb0", "/vendor", 0};
	for (int i = 0; devs[i]; i++) {
		int fd = open(devs[i], 0);
		say("  %s: %s\n", devs[i], fd >= 0 ? "present" : "MISSING");
		if (fd >= 0) close(fd);
	}
}

int main(int argc, char **argv) {
	(void)argc; (void)argv;
	say("gltest: OpenGL ES on the framebuffer through Android's own drivers\n");
	void *ui = lib("libui.so"), *egl = lib("libEGL.so"), *gl = lib("libGLESv2.so");

	EGLNativeWindowType (*createDisplaySurface)(void) = sym(ui, "android_createDisplaySurface");
	EGLDisplay (*eglGetDisplay)(void *) = sym(egl, "eglGetDisplay");
	int (*eglInitialize)(EGLDisplay, EGLint *, EGLint *) = sym(egl, "eglInitialize");
	int (*eglChooseConfig)(EGLDisplay, const EGLint *, EGLConfig *, EGLint, EGLint *) = sym(egl, "eglChooseConfig");
	EGLSurface (*eglCreateWindowSurface)(EGLDisplay, EGLConfig, EGLNativeWindowType, const EGLint *) = sym(egl, "eglCreateWindowSurface");
	EGLContext (*eglCreateContext)(EGLDisplay, EGLConfig, EGLContext, const EGLint *) = sym(egl, "eglCreateContext");
	int (*eglMakeCurrent)(EGLDisplay, EGLSurface, EGLSurface, EGLContext) = sym(egl, "eglMakeCurrent");
	int (*eglSwapBuffers)(EGLDisplay, EGLSurface) = sym(egl, "eglSwapBuffers");
	int (*eglSwapInterval)(EGLDisplay, EGLint) = sym(egl, "eglSwapInterval");
	int (*eglQuerySurface)(EGLDisplay, EGLSurface, EGLint, EGLint *) = sym(egl, "eglQuerySurface");
	EGLint (*eglGetError)(void) = sym(egl, "eglGetError");
	int (*eglTerminate)(EGLDisplay) = sym(egl, "eglTerminate");

	void (*glClearColor)(float, float, float, float) = sym(gl, "glClearColor");
	void (*glClear)(u32) = sym(gl, "glClear");
	void (*glFinish)(void) = sym(gl, "glFinish");
	const char *(*glGetString)(u32) = sym(gl, "glGetString");
	void (*glViewport)(int, int, int, int) = sym(gl, "glViewport");
	u32 (*glCreateShader)(u32) = sym(gl, "glCreateShader");
	void (*glShaderSource)(u32, int, const char **, const int *) = sym(gl, "glShaderSource");
	void (*glCompileShader)(u32) = sym(gl, "glCompileShader");
	u32 (*glCreateProgram)(void) = sym(gl, "glCreateProgram");
	void (*glAttachShader)(u32, u32) = sym(gl, "glAttachShader");
	void (*glLinkProgram)(u32) = sym(gl, "glLinkProgram");
	void (*glUseProgram)(u32) = sym(gl, "glUseProgram");
	void (*glBindAttribLocation)(u32, u32, const char *) = sym(gl, "glBindAttribLocation");
	int (*glGetUniformLocation)(u32, const char *) = sym(gl, "glGetUniformLocation");
	void (*glUniform1f)(int, float) = sym(gl, "glUniform1f");
	void (*glVertexAttribPointer)(u32, int, u32, unsigned char, int, const void *) = sym(gl, "glVertexAttribPointer");
	void (*glEnableVertexAttribArray)(u32) = sym(gl, "glEnableVertexAttribArray");
	void (*glDrawArrays)(u32, int, int) = sym(gl, "glDrawArrays");
	void (*glGenTextures)(int, u32 *) = sym(gl, "glGenTextures");
	void (*glBindTexture)(u32, u32) = sym(gl, "glBindTexture");
	void (*glTexParameteri)(u32, u32, int) = sym(gl, "glTexParameteri");
	void (*glTexImage2D)(u32, int, int, int, int, int, u32, u32, const void *) = sym(gl, "glTexImage2D");
	void (*glGetShaderiv)(u32, u32, int *) = sym(gl, "glGetShaderiv");

	EGLNativeWindowType win = createDisplaySurface();
	if (!win) { say("FAIL: android_createDisplaySurface returned nothing (is SurfaceFlinger still running? stop surfaceflinger)\n"); unpan(); return 1; }
	EGLDisplay dpy = eglGetDisplay(0);
	EGLint maj = 0, min = 0;
	if (!dpy || !eglInitialize(dpy, &maj, &min)) {
		say("FAIL: the GPU driver didn't start (display %p, EGL error 0x%x). Looking for why:\n", dpy, eglGetError());
		diagnose();
		unpan();
		return 1;
	}
	const EGLint cfgAttr[] = {0x3033 /*SURFACE_TYPE*/, 4 /*WINDOW*/, 0x3040 /*RENDERABLE*/, 4 /*ES2*/,
		0x3024, 8, 0x3023, 8, 0x3022, 8, 0x3038};
	EGLConfig cfg; EGLint n = 0;
	if (!eglChooseConfig(dpy, cfgAttr, &cfg, 1, &n) || n < 1) { say("FAIL: no EGL config 0x%x\n", eglGetError()); unpan(); return 1; }
	EGLSurface surf = eglCreateWindowSurface(dpy, cfg, win, 0);
	if (!surf) { say("FAIL: eglCreateWindowSurface 0x%x\n", eglGetError()); unpan(); return 1; }
	const EGLint ctxAttr[] = {0x3098, 2, 0x3038};
	EGLContext ctx = eglCreateContext(dpy, cfg, 0, ctxAttr);
	if (!ctx || !eglMakeCurrent(dpy, surf, surf, ctx)) { say("FAIL: context 0x%x\n", eglGetError()); unpan(); return 1; }
	eglSwapInterval(dpy, 1);
	EGLint w = 0, h = 0;
	eglQuerySurface(dpy, surf, 0x3057, &w);
	eglQuerySurface(dpy, surf, 0x3056, &h);
	say("EGL %d.%d  renderer: %s  version: %s  surface %dx%d\n", maj, min,
		glGetString(0x1F01), glGetString(0x1F02), w, h);
	glViewport(0, 0, w, h);

	/* 1. The swap rate. */
	double t0 = now();
	for (int i = 0; i < 120; i++) {
		glClearColor((i % 3) == 0, (i % 3) == 1, (i % 3) == 2, 1);
		glClear(0x4000);
		eglSwapBuffers(dpy, surf);
	}
	double fps1 = 120 / (now() - t0);
	say("1. clear + swap: %.1f frames/s\n", fps1);

	/* 2. Upload a full-screen image. */
	int tw = w, th = h;
	unsigned char *pix = malloc(tw * th * 4);
	for (int y = 0; y < th; y++)
		for (int x = 0; x < tw; x++) {
			unsigned char *p = pix + 4 * (y * tw + x);
			p[0] = x * 255 / tw; p[1] = y * 255 / th; p[2] = ((x / 64 + y / 64) & 1) * 255; p[3] = 255;
		}
	u32 tex;
	glGenTextures(1, &tex);
	glBindTexture(0x0DE1, tex);
	glTexParameteri(0x0DE1, 0x2801, 0x2601);
	glTexParameteri(0x0DE1, 0x2800, 0x2601);
	glTexParameteri(0x0DE1, 0x2802, 0x812F);
	glTexParameteri(0x0DE1, 0x2803, 0x812F);
	t0 = now();
	for (int i = 0; i < 3; i++) {
		glTexImage2D(0x0DE1, 0, 0x1908, tw, th, 0, 0x1908, 0x1401, pix);
		glFinish();
	}
	double up = (now() - t0) / 3 * 1000;
	say("2. full-screen texture upload: %.1f ms\n", up);

	/* 3. Slide that image across the screen, as a page turn would. */
	const char *vs = "attribute vec2 p; uniform float o; varying vec2 t;"
		"void main(){ t = vec2(p.x*0.5+0.5, 0.5-p.y*0.5); gl_Position = vec4(p.x*1.0 + o, p.y, 0.0, 1.0); }";
	const char *fs = "precision mediump float; varying vec2 t; uniform sampler2D s;"
		"void main(){ gl_FragColor = texture2D(s, t); }";
	u32 v = glCreateShader(0x8B31), f = glCreateShader(0x8B30);
	glShaderSource(v, 1, &vs, 0); glCompileShader(v);
	glShaderSource(f, 1, &fs, 0); glCompileShader(f);
	int ok = 0;
	glGetShaderiv(f, 0x8B81, &ok);
	if (!ok) say("warning: fragment shader didn't compile\n");
	u32 prog = glCreateProgram();
	glAttachShader(prog, v); glAttachShader(prog, f);
	glBindAttribLocation(prog, 0, "p");
	glLinkProgram(prog);
	glUseProgram(prog);
	int uo = glGetUniformLocation(prog, "o");
	static const float quad[] = {-1, -1, 1, -1, -1, 1, 1, 1};
	glVertexAttribPointer(0, 2, 0x1406 /*FLOAT*/, 0, 0, quad);
	glEnableVertexAttribArray(0);
	t0 = now();
	for (int i = 0; i < 120; i++) {
		glClearColor(0, 0, 0, 1);
		glClear(0x4000);
		glUniform1f(uo, 2.0f * (i % 60) / 60.0f);
		glDrawArrays(5 /*TRIANGLE_STRIP*/, 0, 4);
		eglSwapBuffers(dpy, surf);
	}
	double fps3 = 120 / (now() - t0);
	say("3. sliding a full-screen image: %.1f frames/s\n", fps3);

	eglMakeCurrent(dpy, 0, 0, 0);
	eglTerminate(dpy);
	unpan();
	say("RESULT: swap %.0f fps, upload %.0f ms, slide %.0f fps -> %s\n", fps1, up, fps3,
		fps3 >= 50 ? "the GPU can animate condor at full speed" : "the GPU works but is slow here");
	return 0;
}
