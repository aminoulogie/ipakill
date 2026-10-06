/*
 * condorsf: condor's screen on the GPU, through SurfaceFlinger.
 *
 * On this Intel tablet only SurfaceFlinger reaches the panel (glsf.cpp proved it: 58-59
 * frames a second). condor-init starts SurfaceFlinger and this helper, which asks it for a
 * full-screen layer and keeps it for as long as condor runs. condor-init still draws its
 * screen with the processor, into shared memory; the helper uploads the rows that changed and
 * shows them. What the processor can't do quickly, the GPU does: a page taller than the screen
 * is uploaded once and scrolled by moving a quad (60 frames a second under the finger), and
 * the screen changes animate as quads of the before and after pictures (as glanim did).
 *
 *   condorsf <shared memory file> <width> <height> <page rows>
 *
 * Shared memory: three screen images in the framebuffer's native layout (width x height,
 * B G R X bytes): 0 the screen before a change, 1 after, 2 the screen itself; then the page,
 * logical RGBA rows of `height` pixels (the portrait width), up to <page rows> of them.
 *
 * stdin, little-endian 32-bit words; each command is answered with one byte, 'k' (done) or
 * 'f' (couldn't, nothing changed):
 *   1 mask        upload whole images (bit 0 image 0, bit 1 image 1, bit 2 the screen)
 *   2 n quads     draw a frame of n quads and show it (3: the same)
 *   4 lo hi       the screen's native rows lo..hi changed: upload them, show the screen
 *                 (with the overlay on top, if there is one)
 *   5 h           upload the page, h rows
 *   6 y0 y1       the page's rows y0..y1-1 changed: upload them
 *   7 n quads     the overlay: quads drawn over the screen, from now on; show it
 *   8             no overlay (doesn't draw: the next 4 shows the screen alone)
 * A quad is 16 words: kind, then floats x0 y0 x1 y1, u0 v0 u1 v1, a0 a1, mul, r g b, mix.
 *   kind 0..2   image: x y are native screen pixels, u v the image's native pixels
 *   kind 3      colour r g b, opacity a0 at y0 to a1 at y1 (native)
 *   kind 4      the page: x y are LOGICAL screen pixels, u v the page's logical pixels
 * It writes 'R' once the layer is up, and exits on end of input or any error (saying why on
 * stderr), which takes the layer away.
 *
 * Built like glsf (see build.sh and glsf.cpp for the C++ calling details).
 */
typedef unsigned int u32;
typedef int EGLint;
typedef void *EGLDisplay, *EGLConfig, *EGLSurface, *EGLContext;

extern "C" {
int snprintf(char *, unsigned, const char *, ...);
long write(int, const void *, unsigned);
long read(int, void *, unsigned);
int open(const char *, int, ...);
void *mmap(void *, unsigned, int, int, int, long);
void *malloc(unsigned);
void *memset(void *, int, unsigned);
void *dlopen(const char *, int);
void *dlsym(void *, const char *);
const char *dlerror(void);
void exit(int);
}

static char out[512];
#define say(...) do { int n_ = snprintf(out, sizeof out, "condorsf: " __VA_ARGS__); write(2, out, n_); } while (0)
#define fail(...) do { say(__VA_ARGS__); exit(1); } while (0)

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
static void *need(void *h, const char *sym) {
	void *p = dlsym(h, sym);
	if (!p) fail("no %s\n", sym);
	return p;
}
static int num(const char *s) {
	int n = 0;
	while (*s >= '0' && *s <= '9') n = n * 10 + (*s++ - '0');
	return n;
}
static void readAll(void *p, unsigned n) {
	char *b = (char *)p;
	while (n > 0) {
		long r = read(0, b, n);
		if (r <= 0) exit(0); /* condor-init has gone, or closed us */
		b += r;
		n -= r;
	}
}
static void reply(char c) { write(1, &c, 1); }

struct quad { int kind; float x0, y0, x1, y1, u0, v0, u1, v1, a0, a1, mul, r, g, b, mix; };
#define MAXQ 64
#define MAXCHUNK 16

/* GL entry points, filled in main. */
static void (*glViewport)(int, int, int, int);
static void (*glUniform1i)(int, int);
static void (*glUniform1f)(int, float);
static void (*glUniform2f)(int, float, float);
static void (*glUniform3f)(int, float, float, float);
static void (*glVertexAttribPointer)(u32, int, u32, unsigned char, int, const void *);
static void (*glDrawArrays)(u32, int, int);
static void (*glBindTexture)(u32, u32);
static void (*glTexImage2D)(u32, int, int, int, int, int, u32, u32, const void *);
static void (*glTexSubImage2D)(u32, int, int, int, int, int, u32, u32, const void *);
static void (*glEnable)(u32);
static void (*glDisable)(u32);
static void (*glClear)(u32);
static u32 (*glGetError)(void);

static int W, H;                  /* native screen */
static unsigned size;             /* one native image */
static unsigned char *mem;
static unsigned char *pageMem;
static int pageW, pageMax;        /* logical page width (= H), rows it may have */
static u32 tex[3], ptex[MAXCHUNK];
static int loaded[3];
static int chunk, pageH, nChunks; /* page textures: chunk rows each, the last one shorter */
static int uTsize, uSolid, uNative, uMul, uTint, uMix;

static void verts(const float v[4][5]) {
	glVertexAttribPointer(0, 2, 0x1406, 0, 20, &v[0][0]);
	glVertexAttribPointer(1, 2, 0x1406, 0, 20, &v[0][2]);
	glVertexAttribPointer(2, 1, 0x1406, 0, 20, &v[0][4]);
	glDrawArrays(5 /* TRIANGLE_STRIP */, 0, 4);
}

static void drawQuad(const quad *k) {
	glUniform3f(uTint, k->r, k->g, k->b);
	if (k->kind == 3) {
		float v[4][5] = {{k->x0, k->y0, 0, 0, k->a0}, {k->x1, k->y0, 0, 0, k->a0},
		                 {k->x0, k->y1, 0, 0, k->a1}, {k->x1, k->y1, 0, 0, k->a1}};
		glUniform1i(uSolid, 1);
		glEnable(0x0BE2 /* BLEND */);
		verts(v);
		glDisable(0x0BE2);
		return;
	}
	glUniform1i(uSolid, 0);
	glUniform1f(uMul, k->mul);
	glUniform1f(uMix, k->mix);
	if (k->kind >= 0 && k->kind <= 2) {
		if (!loaded[k->kind]) return;
		glBindTexture(0x0DE1, tex[k->kind]);
		glUniform1i(uNative, 1);
		glUniform2f(uTsize, W, H);
		float v[4][5] = {{k->x0, k->y0, k->u0, k->v0, 1}, {k->x1, k->y0, k->u1, k->v0, 1},
		                 {k->x0, k->y1, k->u0, k->v1, 1}, {k->x1, k->y1, k->u1, k->v1, 1}};
		verts(v);
		return;
	}
	if (k->kind != 4) fail("quad kind %d\n", k->kind);
	/* The page: logical (lx, ly) on the screen is native (ly, H - lx) at pixel edges; split
	   across the page's textures, chunk rows each. */
	if (pageH == 0 || k->v1 <= k->v0) return;
	glUniform1i(uNative, 0);
	for (int c = 0; c < nChunks; c++) {
		float cs = (float)c * chunk, ce = cs + (c == nChunks - 1 ? pageH - c * chunk : chunk);
		float a = k->v0 > cs ? k->v0 : cs, b = k->v1 < ce ? k->v1 : ce;
		if (a >= b) continue;
		float s = (k->y1 - k->y0) / (k->v1 - k->v0);
		float ly0 = k->y0 + (a - k->v0) * s, ly1 = k->y0 + (b - k->v0) * s;
		glBindTexture(0x0DE1, ptex[c]);
		glUniform2f(uTsize, pageW, ce - cs);
		float v[4][5] = {{ly0, H - k->x0, k->u0, a - cs, 1}, {ly0, H - k->x1, k->u1, a - cs, 1},
		                 {ly1, H - k->x0, k->u0, b - cs, 1}, {ly1, H - k->x1, k->u1, b - cs, 1}};
		verts(v);
	}
}

/* uploadRows puts native rows lo..hi of image i into its texture. */
static void uploadRows(int i, int lo, int hi) {
	glBindTexture(0x0DE1, tex[i]);
	if (!loaded[i] || (lo <= 0 && hi >= H - 1)) {
		if (loaded[i])
			glTexSubImage2D(0x0DE1, 0, 0, 0, W, H, 0x1908, 0x1401, mem + i * size);
		else
			glTexImage2D(0x0DE1, 0, 0x1908, W, H, 0, 0x1908, 0x1401, mem + i * size);
		loaded[i] = 1;
		return;
	}
	if (lo < 0) lo = 0;
	if (hi > H - 1) hi = H - 1;
	if (lo > hi) return;
	glTexSubImage2D(0x0DE1, 0, 0, lo, W, hi - lo + 1, 0x1908, 0x1401, mem + i * size + (unsigned)lo * W * 4);
}

static int pageUpload(int h) {
	if (h <= 0 || h > pageMax) return 0;
	int n = (h + chunk - 1) / chunk;
	if (n > MAXCHUNK) return 0;
	while (glGetError() != 0) {}
	for (int c = 0; c < n; c++) {
		int rows = c == n - 1 ? h - c * chunk : chunk;
		glBindTexture(0x0DE1, ptex[c]);
		glTexImage2D(0x0DE1, 0, 0x1908, pageW, rows, 0, 0x1908, 0x1401, pageMem + (unsigned)c * chunk * pageW * 4);
	}
	if (glGetError() != 0) { pageH = 0; return 0; }
	pageH = h;
	nChunks = n;
	return 1;
}

static int pageRows(int y0, int y1) {
	if (pageH == 0) return 0;
	if (y0 < 0) y0 = 0;
	if (y1 > pageH) y1 = pageH;
	for (int c = 0; c < nChunks; c++) {
		int cs = c * chunk, ce = cs + (c == nChunks - 1 ? pageH - cs : chunk);
		int a = y0 > cs ? y0 : cs, b = y1 < ce ? y1 : ce;
		if (a >= b) continue;
		glBindTexture(0x0DE1, ptex[c]);
		glTexSubImage2D(0x0DE1, 0, 0, a - cs, pageW, b - a, 0x1908, 0x1401, pageMem + (unsigned)a * pageW * 4);
	}
	return 1;
}

int main(int argc, char **argv) {
	if (argc < 5) fail("usage: condorsf <shm file> <width> <height> <page rows>\n");
	W = num(argv[2]);
	H = num(argv[3]);
	pageMax = num(argv[4]);
	pageW = H;
	size = (unsigned)W * H * 4;
	int shm = open(argv[1], 2 /* O_RDWR */);
	if (shm < 0) fail("can't open %s\n", argv[1]);
	mem = (unsigned char *)mmap(0, 3 * size + (unsigned)pageW * pageMax * 4, 3, 1, shm, 0);
	if (mem == (unsigned char *)-1) fail("mmap %s\n", argv[1]);
	pageMem = mem + 3 * size;

	void *utils = lib("libutils.so"), *binder = lib("libbinder.so"), *gui = lib("libgui.so");
	void *egl = lib("libEGL.so"), *gl = lib("libGLESv2.so");

	/* SurfaceFlinger's client side (Android 4.2.2), as glsf.cpp does it. */
	typedef void (*ctor_t)(void *);
	typedef void (*ref_t)(const void *, const void *);
	typedef void (*str8_t)(void *, const char *);
	typedef sp (*self_t)();
	typedef void (*void_t)(void *);
	typedef sp (*create_t)(void *, const void *, u32, u32, int, u32);
	typedef void (*static_t)();
	typedef void (*close_t)(bool);
	typedef int (*setlayer_t)(void *, int);
	typedef int (*show_t)(void *);
	typedef sp (*getsurface_t)(const void *);
	str8_t String8 = (str8_t)need(utils, "_ZN7android7String8C1EPKc");
	ref_t incStrong = (ref_t)need(utils, "_ZNK7android7RefBase9incStrongEPKv");
	self_t procSelf = (self_t)need(binder, "_ZN7android12ProcessState4selfEv");
	void_t startThreadPool = (void_t)need(binder, "_ZN7android12ProcessState15startThreadPoolEv");
	ctor_t newClient = (ctor_t)need(gui, "_ZN7android21SurfaceComposerClientC1Ev");
	create_t createSurface = (create_t)need(gui, "_ZN7android21SurfaceComposerClient13createSurfaceERKNS_7String8Ejjij");
	static_t openTx = (static_t)need(gui, "_ZN7android21SurfaceComposerClient21openGlobalTransactionEv");
	close_t closeTx = (close_t)need(gui, "_ZN7android21SurfaceComposerClient22closeGlobalTransactionEb");
	setlayer_t setLayer = (setlayer_t)need(gui, "_ZN7android14SurfaceControl8setLayerEi");
	show_t show = (show_t)need(gui, "_ZN7android14SurfaceControl4showEv");
	getsurface_t getSurface = (getsurface_t)need(gui, "_ZNK7android14SurfaceControl10getSurfaceEv");

	sp proc = procSelf();
	if (!proc.p) fail("ProcessState::self() returned nothing\n");
	startThreadPool(proc.p);
	void *client = malloc(1024);
	memset(client, 0, 1024);
	newClient(client);
	incStrong(client, client); /* connects to SurfaceFlinger (waits for it to be up) */
	char name[16];
	String8(name, "condor");
	sp control = createSurface(client, name, W, H, 2 /* RGBX_8888 */, 0x400 /* eOpaque */);
	if (!control.p) fail("createSurface returned nothing (is SurfaceFlinger running?)\n");
	openTx();
	setLayer(control.p, 0x40000001); /* above the boot animation, should it still be up */
	show(control.p);
	closeTx(false);
	sp surface = getSurface(control.p);
	if (!surface.p) fail("getSurface returned nothing\n");
	/* EGL wants the ANativeWindow inside the Surface: find it by its magic word, '_wnd'. */
	void *window = 0;
	for (int off = 0; off <= 64; off += 4)
		if (*(u32 *)((char *)surface.p + off) == 0x5f776e64) { window = (char *)surface.p + off; break; }
	if (!window) fail("no ANativeWindow inside the Surface\n");

	typedef EGLDisplay (*getdpy_t)(void *);
	typedef int (*init_t)(EGLDisplay, EGLint *, EGLint *);
	typedef int (*choose_t)(EGLDisplay, const EGLint *, EGLConfig *, EGLint, EGLint *);
	typedef EGLSurface (*winsurf_t)(EGLDisplay, EGLConfig, void *, const EGLint *);
	typedef EGLContext (*ctx_t)(EGLDisplay, EGLConfig, EGLContext, const EGLint *);
	typedef int (*make_t)(EGLDisplay, EGLSurface, EGLSurface, EGLContext);
	typedef int (*swap_t)(EGLDisplay, EGLSurface);
	typedef int (*query_t)(EGLDisplay, EGLSurface, EGLint, EGLint *);
	typedef EGLint (*err_t)();
	getdpy_t eglGetDisplay = (getdpy_t)need(egl, "eglGetDisplay");
	init_t eglInitialize = (init_t)need(egl, "eglInitialize");
	choose_t eglChooseConfig = (choose_t)need(egl, "eglChooseConfig");
	winsurf_t eglCreateWindowSurface = (winsurf_t)need(egl, "eglCreateWindowSurface");
	ctx_t eglCreateContext = (ctx_t)need(egl, "eglCreateContext");
	make_t eglMakeCurrent = (make_t)need(egl, "eglMakeCurrent");
	swap_t eglSwapBuffers = (swap_t)need(egl, "eglSwapBuffers");
	query_t eglQuerySurface = (query_t)need(egl, "eglQuerySurface");
	err_t eglGetError = (err_t)need(egl, "eglGetError");

	EGLDisplay dpy = eglGetDisplay(0);
	if (!eglInitialize(dpy, 0, 0)) fail("eglInitialize 0x%x\n", eglGetError());
	const EGLint cfgAttr[] = {0x3033, 4, 0x3040, 4, 0x3024, 8, 0x3023, 8, 0x3022, 8, 0x3038};
	EGLConfig cfg;
	EGLint n = 0;
	if (!eglChooseConfig(dpy, cfgAttr, &cfg, 1, &n) || n < 1) fail("eglChooseConfig 0x%x\n", eglGetError());
	EGLSurface surf = eglCreateWindowSurface(dpy, cfg, window, 0);
	if (!surf) fail("eglCreateWindowSurface 0x%x\n", eglGetError());
	const EGLint ctxAttr[] = {0x3098, 2, 0x3038};
	EGLContext ctx = eglCreateContext(dpy, cfg, 0, ctxAttr);
	if (!ctx || !eglMakeCurrent(dpy, surf, surf, ctx)) fail("context 0x%x\n", eglGetError());
	EGLint sw = 0, sh = 0;
	eglQuerySurface(dpy, surf, 0x3057, &sw);
	eglQuerySurface(dpy, surf, 0x3056, &sh);
	if (sw != W || sh != H) fail("the layer is %dx%d, not %dx%d\n", sw, sh, W, H);

	glViewport = (void (*)(int, int, int, int))need(gl, "glViewport");
	u32 (*glCreateShader)(u32) = (u32 (*)(u32))need(gl, "glCreateShader");
	void (*glShaderSource)(u32, int, const char **, const int *) = (void (*)(u32, int, const char **, const int *))need(gl, "glShaderSource");
	void (*glCompileShader)(u32) = (void (*)(u32))need(gl, "glCompileShader");
	void (*glGetShaderiv)(u32, u32, int *) = (void (*)(u32, u32, int *))need(gl, "glGetShaderiv");
	void (*glGetShaderInfoLog)(u32, int, int *, char *) = (void (*)(u32, int, int *, char *))need(gl, "glGetShaderInfoLog");
	u32 (*glCreateProgram)() = (u32 (*)())need(gl, "glCreateProgram");
	void (*glAttachShader)(u32, u32) = (void (*)(u32, u32))need(gl, "glAttachShader");
	void (*glBindAttribLocation)(u32, u32, const char *) = (void (*)(u32, u32, const char *))need(gl, "glBindAttribLocation");
	void (*glLinkProgram)(u32) = (void (*)(u32))need(gl, "glLinkProgram");
	void (*glUseProgram)(u32) = (void (*)(u32))need(gl, "glUseProgram");
	int (*glGetUniformLocation)(u32, const char *) = (int (*)(u32, const char *))need(gl, "glGetUniformLocation");
	void (*glEnableVertexAttribArray)(u32) = (void (*)(u32))need(gl, "glEnableVertexAttribArray");
	void (*glGenTextures)(int, u32 *) = (void (*)(int, u32 *))need(gl, "glGenTextures");
	void (*glTexParameteri)(u32, u32, int) = (void (*)(u32, u32, int))need(gl, "glTexParameteri");
	void (*glBlendFunc)(u32, u32) = (void (*)(u32, u32))need(gl, "glBlendFunc");
	void (*glClearColor)(float, float, float, float) = (void (*)(float, float, float, float))need(gl, "glClearColor");
	void (*glGetIntegerv)(u32, int *) = (void (*)(u32, int *))need(gl, "glGetIntegerv");
	glUniform1i = (void (*)(int, int))need(gl, "glUniform1i");
	glUniform1f = (void (*)(int, float))need(gl, "glUniform1f");
	glUniform2f = (void (*)(int, float, float))need(gl, "glUniform2f");
	glUniform3f = (void (*)(int, float, float, float))need(gl, "glUniform3f");
	glVertexAttribPointer = (void (*)(u32, int, u32, unsigned char, int, const void *))need(gl, "glVertexAttribPointer");
	glDrawArrays = (void (*)(u32, int, int))need(gl, "glDrawArrays");
	glBindTexture = (void (*)(u32, u32))need(gl, "glBindTexture");
	glTexImage2D = (void (*)(u32, int, int, int, int, int, u32, u32, const void *))need(gl, "glTexImage2D");
	glTexSubImage2D = (void (*)(u32, int, int, int, int, int, u32, u32, const void *))need(gl, "glTexSubImage2D");
	glEnable = (void (*)(u32))need(gl, "glEnable");
	glDisable = (void (*)(u32))need(gl, "glDisable");
	glClear = (void (*)(u32))need(gl, "glClear");
	glGetError = (u32 (*)())need(gl, "glGetError");
	glViewport(0, 0, W, H);

	/* Screen images come as B G R X bytes, uploaded as RGBA: their colour is the texel's .bgr.
	   The page is RGBA: its colour is .rgb. */
	const char *vs =
		"attribute vec2 p; attribute vec2 t; attribute float a; uniform vec2 size; uniform vec2 tsize;"
		"varying highp vec2 uv; varying float al;"
		"void main(){ uv = t / tsize; al = a;"
		" gl_Position = vec4(p.x / size.x * 2.0 - 1.0, 1.0 - p.y / size.y * 2.0, 0.0, 1.0); }";
	const char *fs =
		"precision highp float; uniform sampler2D img; uniform int solid; uniform int native;"
		"uniform float mul; uniform vec3 tint; uniform float mixk; varying highp vec2 uv; varying float al;"
		"void main(){ if (solid == 1) { gl_FragColor = vec4(tint, al); return; }"
		" vec4 s = texture2D(img, uv); vec3 c = native == 1 ? s.bgr : s.rgb;"
		" gl_FragColor = vec4(mix(c, tint, mixk) * mul, 1.0); }";
	const char *src[2] = {vs, fs};
	u32 sh2[2];
	for (int i = 0; i < 2; i++) {
		sh2[i] = glCreateShader(i == 0 ? 0x8B31 : 0x8B30);
		glShaderSource(sh2[i], 1, &src[i], 0);
		glCompileShader(sh2[i]);
		int ok = 0;
		glGetShaderiv(sh2[i], 0x8B81, &ok);
		if (!ok) {
			char log[300];
			int ln = 0;
			glGetShaderInfoLog(sh2[i], sizeof log - 1, &ln, log);
			log[ln] = 0;
			fail("shader: %s\n", log);
		}
	}
	u32 prog = glCreateProgram();
	glAttachShader(prog, sh2[0]);
	glAttachShader(prog, sh2[1]);
	glBindAttribLocation(prog, 0, "p");
	glBindAttribLocation(prog, 1, "t");
	glBindAttribLocation(prog, 2, "a");
	glLinkProgram(prog);
	glUseProgram(prog);
	glUniform2f(glGetUniformLocation(prog, "size"), W, H);
	glUniform1i(glGetUniformLocation(prog, "img"), 0);
	uTsize = glGetUniformLocation(prog, "tsize");
	uSolid = glGetUniformLocation(prog, "solid");
	uNative = glGetUniformLocation(prog, "native");
	uMul = glGetUniformLocation(prog, "mul");
	uTint = glGetUniformLocation(prog, "tint");
	uMix = glGetUniformLocation(prog, "mixk");
	glBlendFunc(0x0302 /* SRC_ALPHA */, 0x0303 /* ONE_MINUS_SRC_ALPHA */);
	glEnableVertexAttribArray(0);
	glEnableVertexAttribArray(1);
	glEnableVertexAttribArray(2);

	int maxTex = 0;
	glGetIntegerv(0x0D33 /* MAX_TEXTURE_SIZE */, &maxTex);
	chunk = maxTex >= 4096 ? 4096 : maxTex >= 2048 ? 2048 : 1024;
	glGenTextures(3, tex);
	glGenTextures(MAXCHUNK, ptex);
	for (int i = 0; i < 3 + MAXCHUNK; i++) {
		glBindTexture(0x0DE1, i < 3 ? tex[i] : ptex[i - 3]);
		glTexParameteri(0x0DE1, 0x2801, 0x2600); /* NEAREST: whole pixels, exact copies */
		glTexParameteri(0x0DE1, 0x2800, 0x2600);
		glTexParameteri(0x0DE1, 0x2802, 0x812F); /* CLAMP_TO_EDGE (needed for any size) */
		glTexParameteri(0x0DE1, 0x2803, 0x812F);
	}
	glClearColor(0, 0, 0, 1);
	glClear(0x4000);
	eglSwapBuffers(dpy, surf);
	say("layer up: %dx%d, page textures of %d rows (GPU max %d)\n", W, H, chunk, maxTex);
	reply('R');

	static quad q[MAXQ], over[MAXQ];
	int nOver = 0;
	const quad screen = {2, 0, 0, (float)W, (float)H, 0, 0, (float)W, (float)H, 1, 1, 1, 0, 0, 0, 0};
	for (;;) {
		u32 hdr[2];
		readAll(hdr, 4);
		u32 cmd = hdr[0];
		if (cmd == 8) { nOver = 0; reply('k'); continue; }
		readAll(&hdr[1], 4);
		switch (cmd) {
		case 1:
			for (int i = 0; i < 3; i++)
				if (hdr[1] & (1u << i)) uploadRows(i, 0, H - 1);
			reply('k');
			continue;
		case 5:
			reply(pageUpload((int)hdr[1]) ? 'k' : 'f');
			continue;
		case 2: case 3: case 7: {
			if (hdr[1] > MAXQ) fail("%u quads\n", hdr[1]);
			quad *dst = cmd == 7 ? over : q;
			readAll(dst, hdr[1] * sizeof(quad));
			glClear(0x4000);
			if (cmd == 7) {
				nOver = (int)hdr[1];
				drawQuad(&screen);
				for (int i = 0; i < nOver; i++) drawQuad(&over[i]);
			} else {
				for (u32 i = 0; i < hdr[1]; i++) drawQuad(&q[i]);
			}
			if (!eglSwapBuffers(dpy, surf)) fail("eglSwapBuffers 0x%x\n", eglGetError());
			reply('k');
			continue;
		}
		case 4: case 6: {
			u32 b;
			readAll(&b, 4);
			if (cmd == 6) { reply(pageRows((int)hdr[1], (int)b) ? 'k' : 'f'); continue; }
			uploadRows(2, (int)hdr[1], (int)b);
			glClear(0x4000);
			drawQuad(&screen);
			for (int i = 0; i < nOver; i++) drawQuad(&over[i]);
			if (!eglSwapBuffers(dpy, surf)) fail("eglSwapBuffers 0x%x\n", eglGetError());
			reply('k');
			continue;
		}
		}
		fail("unknown command %u\n", cmd);
	}
}
