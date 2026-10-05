/*
 * glsf: the GPU through SurfaceFlinger, the way Android's own boot animation draws
 * (frameworks/base/cmds/bootanimation/BootAnimation.cpp, Android 4.2.2).
 *
 * glanim drew through the raw framebuffer window and nothing reached the panel: on this
 * Intel Clover Trail+ tablet the display is driven by Intel's hardware composer, which only
 * SurfaceFlinger talks to. So this asks SurfaceFlinger for a full-screen layer, like the boot
 * animation does, and draws into it with OpenGL ES 2.
 *
 * Built without the NDK or Android's headers (see build.sh): the C++ classes it uses are
 * called through their exported symbols, found with dlsym. On i386 the C++ ABI passes `this`
 * as the first argument, and a function returning sp<T> (a class with a destructor) takes a
 * hidden pointer to the result before everything else, which the callee pops; declaring the
 * function pointers to return the sp<> struct below makes clang call them exactly that way.
 *
 * What it shows, to watch on the tablet:
 *   glsf show    6 s of the screen as condor drew it (read from the framebuffer), moved
 *                300 pixels down, then 4 s of red, then 4 s of a moving gradient; it
 *                prints what it did and the frame rate.
 */
typedef unsigned int u32;
typedef int EGLint;
typedef void *EGLDisplay, *EGLConfig, *EGLSurface, *EGLContext;

extern "C" {
int snprintf(char *, unsigned, const char *, ...);
long write(int, const void *, unsigned);
long read(int, void *, unsigned);
int open(const char *, int, ...);
int close(int);
void *malloc(unsigned);
void *memset(void *, int, unsigned);
struct timespec { long tv_sec, tv_nsec; };
int clock_gettime(int, struct timespec *);
void *dlopen(const char *, int);
void *dlsym(void *, const char *);
const char *dlerror(void);
void exit(int);
}

static char out[512];
#define say(...) do { int n_ = snprintf(out, sizeof out, __VA_ARGS__); write(1, out, n_); } while (0)
#define fail(...) do { say("FAIL: " __VA_ARGS__); exit(1); } while (0)

static double now() {
	timespec t;
	clock_gettime(1, &t);
	return t.tv_sec + t.tv_nsec / 1e9;
}

// android::sp<T>: one pointer. The destructor makes it a non-trivial class, so functions
// returning it use the hidden result pointer, as the real (C++) libraries do. It never
// releases anything: this program keeps every object until it exits.
struct sp {
	void *p;
	sp() : p(0) {}
	~sp() {}
};

static void *lib(const char *name) {
	void *h = dlopen(name, 0);
	if (!h) fail("dlopen %s: %s\n", name, dlerror());
	return h;
}

static void *need(void *h, const char *lname, const char *sym) {
	void *p = dlsym(h, sym);
	if (!p) fail("%s has no %s\n", lname, sym);
	return p;
}

int main(int argc, char **argv) {
	(void)argc; (void)argv;
	say("glsf: OpenGL ES through SurfaceFlinger, like Android's boot animation\n");
	say("step: loading libutils, libbinder, libgui, libEGL, libGLESv2\n");
	void *utils = lib("libutils.so"), *binder = lib("libbinder.so"), *gui = lib("libgui.so");
	void *egl = lib("libEGL.so"), *gl = lib("libGLESv2.so");

	// The C++ entry points (Android 4.2.2, frameworks/native), looked up by their names.
	typedef void (*ctor_t)(void *self);
	typedef void (*ref_t)(const void *self, const void *id);
	typedef void (*str8_t)(void *self, const char *s);
	typedef sp (*self_t)();
	typedef void (*void_t)(void *self);
	typedef sp (*create_t)(void *self, const void *name, u32 w, u32 h, int format, u32 flags);
	typedef void (*static_t)();
	typedef void (*close_t)(bool synchronous);
	typedef int (*setlayer_t)(void *self, int layer);
	typedef int (*show_t)(void *self);
	typedef sp (*getsurface_t)(const void *self);

	str8_t String8 = (str8_t)need(utils, "libutils", "_ZN7android7String8C1EPKc");
	ref_t incStrong = (ref_t)need(utils, "libutils", "_ZNK7android7RefBase9incStrongEPKv");
	self_t procSelf = (self_t)need(binder, "libbinder", "_ZN7android12ProcessState4selfEv");
	void_t startThreadPool = (void_t)need(binder, "libbinder", "_ZN7android12ProcessState15startThreadPoolEv");
	ctor_t newClient = (ctor_t)need(gui, "libgui", "_ZN7android21SurfaceComposerClientC1Ev");
	create_t createSurface = (create_t)need(gui, "libgui", "_ZN7android21SurfaceComposerClient13createSurfaceERKNS_7String8Ejjij");
	static_t openTx = (static_t)need(gui, "libgui", "_ZN7android21SurfaceComposerClient21openGlobalTransactionEv");
	close_t closeTx = (close_t)need(gui, "libgui", "_ZN7android21SurfaceComposerClient22closeGlobalTransactionEb");
	setlayer_t setLayer = (setlayer_t)need(gui, "libgui", "_ZN7android14SurfaceControl8setLayerEi");
	show_t show = (show_t)need(gui, "libgui", "_ZN7android14SurfaceControl4showEv");
	getsurface_t getSurface = (getsurface_t)need(gui, "libgui", "_ZNK7android14SurfaceControl10getSurfaceEv");
	say("step: all %d SurfaceFlinger functions found\n", 11);

	// Binder threads, so SurfaceFlinger's answers arrive (the boot animation does the same).
	sp proc = procSelf();
	if (!proc.p) fail("ProcessState::self() returned nothing\n");
	startThreadPool(proc.p);

	say("step: connecting to SurfaceFlinger\n");
	void *client = malloc(1024); // bigger than the class; RefBase is its first (and only) base
	memset(client, 0, 1024);
	newClient(client);
	incStrong(client, client); // the first reference: onFirstRef() connects to SurfaceFlinger

	say("step: asking for a full-screen layer (1920x1200)\n");
	char name[16];
	String8(name, "condor");
	const int PIXEL_FORMAT_RGBX_8888 = 2, eOpaque = 0x400;
	sp control = createSurface(client, name, 1920, 1200, PIXEL_FORMAT_RGBX_8888, eOpaque);
	if (!control.p) fail("createSurface returned nothing (is SurfaceFlinger running?)\n");
	openTx();
	int e1 = setLayer(control.p, 0x40000000);
	int e2 = show(control.p);
	closeTx(false);
	say("step: layer set (%d) and shown (%d)\n", e1, e2);
	sp surface = getSurface(control.p);
	if (!surface.p) fail("getSurface returned nothing\n");

	// EGL on it, as the boot animation does.
	typedef EGLDisplay (*getdpy_t)(void *);
	typedef int (*init_t)(EGLDisplay, EGLint *, EGLint *);
	typedef int (*choose_t)(EGLDisplay, const EGLint *, EGLConfig *, EGLint, EGLint *);
	typedef EGLSurface (*winsurf_t)(EGLDisplay, EGLConfig, void *, const EGLint *);
	typedef EGLContext (*ctx_t)(EGLDisplay, EGLConfig, EGLContext, const EGLint *);
	typedef int (*make_t)(EGLDisplay, EGLSurface, EGLSurface, EGLContext);
	typedef int (*swap_t)(EGLDisplay, EGLSurface);
	typedef int (*query_t)(EGLDisplay, EGLSurface, EGLint, EGLint *);
	typedef EGLint (*err_t)();
	getdpy_t eglGetDisplay = (getdpy_t)need(egl, "libEGL", "eglGetDisplay");
	init_t eglInitialize = (init_t)need(egl, "libEGL", "eglInitialize");
	choose_t eglChooseConfig = (choose_t)need(egl, "libEGL", "eglChooseConfig");
	winsurf_t eglCreateWindowSurface = (winsurf_t)need(egl, "libEGL", "eglCreateWindowSurface");
	ctx_t eglCreateContext = (ctx_t)need(egl, "libEGL", "eglCreateContext");
	make_t eglMakeCurrent = (make_t)need(egl, "libEGL", "eglMakeCurrent");
	swap_t eglSwapBuffers = (swap_t)need(egl, "libEGL", "eglSwapBuffers");
	query_t eglQuerySurface = (query_t)need(egl, "libEGL", "eglQuerySurface");
	err_t eglGetError = (err_t)need(egl, "libEGL", "eglGetError");

	say("step: EGL on the layer\n");
	EGLDisplay dpy = eglGetDisplay(0);
	if (!eglInitialize(dpy, 0, 0)) fail("eglInitialize 0x%x\n", eglGetError());
	const EGLint cfgAttr[] = {0x3033, 4, 0x3040, 4, 0x3024, 8, 0x3023, 8, 0x3022, 8, 0x3038};
	EGLConfig cfg; EGLint n = 0;
	if (!eglChooseConfig(dpy, cfgAttr, &cfg, 1, &n) || n < 1) fail("eglChooseConfig 0x%x\n", eglGetError());
	// eglCreateWindowSurface wants an ANativeWindow*, not the Surface*. In C++ the compiler
	// adds the offset of the ANativeWindow base inside Surface when it converts one to the
	// other (the boot animation passes s.get()); calling through dlsym we must do it by hand.
	// ANativeWindow starts with android_native_base_t, whose first word is the magic '_wnd',
	// so find it inside the object rather than assuming the offset.
	const u32 WND_MAGIC = 0x5f776e64;
	void *window = 0;
	for (int off = 0; off <= 64; off += 4) {
		if (*(u32 *)((char *)surface.p + off) == WND_MAGIC) {
			window = (char *)surface.p + off;
			say("step: ANativeWindow found %d bytes into the Surface\n", off);
			break;
		}
	}
	if (!window) {
		say("step: no ANativeWindow magic in the first 64 bytes of the Surface; words:");
		for (int off = 0; off < 48; off += 4) say(" %08x", *(u32 *)((char *)surface.p + off));
		say("\n");
		fail("cannot find the ANativeWindow inside the Surface\n");
	}
	EGLSurface surf = eglCreateWindowSurface(dpy, cfg, window, 0);
	if (!surf) fail("eglCreateWindowSurface 0x%x\n", eglGetError());
	const EGLint ctxAttr[] = {0x3098, 2, 0x3038};
	EGLContext ctx = eglCreateContext(dpy, cfg, 0, ctxAttr);
	if (!ctx || !eglMakeCurrent(dpy, surf, surf, ctx)) fail("context 0x%x\n", eglGetError());
	EGLint W = 0, H = 0;
	eglQuerySurface(dpy, surf, 0x3057, &W);
	eglQuerySurface(dpy, surf, 0x3056, &H);
	say("step: drawing on a %dx%d SurfaceFlinger layer\n", W, H);

	typedef void (*v4f)(float, float, float, float);
	typedef void (*vu)(u32);
	typedef void (*v4i)(int, int, int, int);
	typedef u32 (*u_u)(u32);
	typedef void (*src_t)(u32, int, const char **, const int *);
	typedef u32 (*u_v)();
	typedef void (*vuu)(u32, u32);
	typedef void (*battr_t)(u32, u32, const char *);
	typedef int (*uloc_t)(u32, const char *);
	typedef void (*u2f)(int, float, float);
	typedef void (*u1i)(int, int);
	typedef void (*vap_t)(u32, int, u32, unsigned char, int, const void *);
	typedef void (*draw_t)(u32, int, int);
	typedef void (*gen_t)(int, u32 *);
	typedef void (*tp_t)(u32, u32, int);
	typedef void (*ti_t)(u32, int, int, int, int, int, u32, u32, const void *);
	typedef void (*rp_t)(int, int, int, int, u32, u32, void *);
	v4f glClearColor = (v4f)need(gl, "libGLESv2", "glClearColor");
	vu glClear = (vu)need(gl, "libGLESv2", "glClear");
	v4i glViewport = (v4i)need(gl, "libGLESv2", "glViewport");
	u_u glCreateShader = (u_u)need(gl, "libGLESv2", "glCreateShader");
	src_t glShaderSource = (src_t)need(gl, "libGLESv2", "glShaderSource");
	vu glCompileShader = (vu)need(gl, "libGLESv2", "glCompileShader");
	u_v glCreateProgram = (u_v)need(gl, "libGLESv2", "glCreateProgram");
	vuu glAttachShader = (vuu)need(gl, "libGLESv2", "glAttachShader");
	battr_t glBindAttribLocation = (battr_t)need(gl, "libGLESv2", "glBindAttribLocation");
	vu glLinkProgram = (vu)need(gl, "libGLESv2", "glLinkProgram");
	vu glUseProgram = (vu)need(gl, "libGLESv2", "glUseProgram");
	uloc_t glGetUniformLocation = (uloc_t)need(gl, "libGLESv2", "glGetUniformLocation");
	u2f glUniform2f = (u2f)need(gl, "libGLESv2", "glUniform2f");
	u1i glUniform1i = (u1i)need(gl, "libGLESv2", "glUniform1i");
	vap_t glVertexAttribPointer = (vap_t)need(gl, "libGLESv2", "glVertexAttribPointer");
	vu glEnableVertexAttribArray = (vu)need(gl, "libGLESv2", "glEnableVertexAttribArray");
	draw_t glDrawArrays = (draw_t)need(gl, "libGLESv2", "glDrawArrays");
	gen_t glGenTextures = (gen_t)need(gl, "libGLESv2", "glGenTextures");
	vuu glBindTexture = (vuu)need(gl, "libGLESv2", "glBindTexture");
	tp_t glTexParameteri = (tp_t)need(gl, "libGLESv2", "glTexParameteri");
	ti_t glTexImage2D = (ti_t)need(gl, "libGLESv2", "glTexImage2D");
	rp_t glReadPixels = (rp_t)need(gl, "libGLESv2", "glReadPixels");
	glViewport(0, 0, W, H);

	// The screen as condor drew it: its framebuffer, B G R X bytes, 1920x1200.
	const int FW = 1920, FH = 1200;
	unsigned char *pix = (unsigned char *)malloc(FW * FH * 4);
	int fb = open("/dev/graphics/fb0", 0);
	unsigned got = 0;
	while (fb >= 0 && got < (unsigned)FW * FH * 4) {
		long r = read(fb, pix + got, FW * FH * 4 - got);
		if (r <= 0) break;
		got += r;
	}
	if (fb >= 0) close(fb);
	say("step: read %u bytes of condor's screen\n", got);

	const char *vs = "attribute vec2 p; uniform vec2 shift; varying highp vec2 uv;"
		"void main(){ uv = vec2(p.x*0.5+0.5, 0.5-p.y*0.5); gl_Position = vec4(p + shift, 0.0, 1.0); }";
	const char *fs = "precision highp float; uniform sampler2D img; uniform int mode; uniform vec2 t;"
		"varying highp vec2 uv; void main(){"
		" if (mode == 1) { gl_FragColor = vec4(1.0, 0.0, 0.0, 1.0); return; }"
		" if (mode == 2) { gl_FragColor = vec4(uv.x, fract(uv.y + t.x), 0.5 + 0.5*sin(t.x*6.28), 1.0); return; }"
		" gl_FragColor = vec4(texture2D(img, uv).bgr, 1.0); }";
	u32 v = glCreateShader(0x8B31), f = glCreateShader(0x8B30);
	glShaderSource(v, 1, &vs, 0); glCompileShader(v);
	glShaderSource(f, 1, &fs, 0); glCompileShader(f);
	u32 prog = glCreateProgram();
	glAttachShader(prog, v); glAttachShader(prog, f);
	glBindAttribLocation(prog, 0, "p");
	glLinkProgram(prog);
	glUseProgram(prog);
	int uShift = glGetUniformLocation(prog, "shift"), uMode = glGetUniformLocation(prog, "mode");
	int uT = glGetUniformLocation(prog, "t");
	static const float quad[] = {-1, -1, 1, -1, -1, 1, 1, 1};
	glVertexAttribPointer(0, 2, 0x1406, 0, 0, quad);
	glEnableVertexAttribArray(0);
	u32 tex;
	glGenTextures(1, &tex);
	glBindTexture(0x0DE1, tex);
	glTexParameteri(0x0DE1, 0x2801, 0x2601);
	glTexParameteri(0x0DE1, 0x2800, 0x2601);
	glTexParameteri(0x0DE1, 0x2802, 0x812F);
	glTexParameteri(0x0DE1, 0x2803, 0x812F);
	glTexImage2D(0x0DE1, 0, 0x1908, FW, FH, 0, 0x1908, 0x1401, pix);

	// 1. condor's screen through the GPU, moved down (300 native columns = right on the panel's
	//    native layout, which is down on the portrait screen).
	say(">>> WATCH THE TABLET: 6 s of condor's screen moved down\n");
	glUniform1i(uMode, 0);
	glUniform2f(uShift, 2.0f * 300 / FW, 0);
	double t0 = now();
	int frames = 0;
	while (now() - t0 < 6) {
		glClearColor(0, 0, 0, 1);
		glClear(0x4000);
		glDrawArrays(5, 0, 4);
		if (frames == 0) {
			unsigned char px[4];
			glReadPixels(W / 2, H / 2, 1, 1, 0x1908, 0x1401, px);
			say("     the GPU drew r%d g%d b%d at the centre\n", px[0], px[1], px[2]);
		}
		eglSwapBuffers(dpy, surf);
		frames++;
	}
	say("     %.1f frames/s\n", frames / (now() - t0));

	// 2. Red.
	say(">>> 4 s of red\n");
	glUniform1i(uMode, 1);
	glUniform2f(uShift, 0, 0);
	t0 = now();
	while (now() - t0 < 4) { glDrawArrays(5, 0, 4); eglSwapBuffers(dpy, surf); }

	// 3. A moving gradient: is it smooth?
	say(">>> 4 s of a moving gradient\n");
	glUniform1i(uMode, 2);
	t0 = now();
	frames = 0;
	while (now() - t0 < 4) {
		glUniform2f(uT, (float)(now() - t0) / 2, 0);
		glDrawArrays(5, 0, 4);
		eglSwapBuffers(dpy, surf);
		frames++;
	}
	say("     %.1f frames/s\n", frames / (now() - t0));
	say("done: the layer goes away as glsf exits\n");
	return 0;
}
