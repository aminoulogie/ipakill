/*
 * glanim: condor's animations on the tablet's GPU (PowerVR SGX544) through Android's own
 * EGL/GLES drivers. condor-init starts it (SurfaceFlinger stopped), embedded in itself.
 *
 *   glanim <shared memory file> <width> <height>
 *
 * The shared memory file (on Android's /dev tmpfs) holds three screen images in the
 * framebuffer's own layout (width x height, 4 bytes a pixel, XRGB little-endian, so the
 * bytes are B G R X). condor-init draws the screen before and after a change into them,
 * then drives the animation one frame at a time on stdin; every frame is a list of quads,
 * each a rectangle of one of the images (or a plain colour) placed somewhere on the screen.
 * The GPU puts the frame together and shows it at the next vertical blank.
 *
 * stdin, little-endian 32-bit words:
 *   1 mask        upload the images whose bit is set (1 = image 0, 2 = 1, 4 = 2)
 *   2 n  quads    draw a frame of n quads and show it
 *   3 n  quads    the last frame: show it, and make sure the display ends up showing the
 *                 framebuffer from its top (where condor-init draws with write())
 * A quad is 16 words: kind (0..2 image, 3 colour), then floats: x0 y0 x1 y1 (corners on the
 * screen, pixels), u0 v0 u1 v1 (the image's pixels at those corners: u0 > u1 mirrors),
 * a0 a1 (colour quads: opacity at y0 and y1), mul (image brightness), r g b (colour, or the
 * tint an image is mixed with), mix (how much of the tint, images).
 * Every command is answered with one byte, 'k'. It writes 'R' once it's ready, and exits on
 * end of input (or any error, after saying why on stderr).
 */
typedef unsigned int u32;
typedef int EGLint;
typedef void *EGLDisplay, *EGLConfig, *EGLSurface, *EGLContext, *EGLNativeWindowType;

extern int snprintf(char *, unsigned, const char *, ...);
extern long write(int, const void *, unsigned);
extern long read(int, void *, unsigned);
extern int open(const char *, int, ...);
extern int ioctl(int, int, ...);
extern int close(int);
extern void *mmap(void *, unsigned, int, int, int, long);
extern void *dlopen(const char *, int);
extern void *dlsym(void *, const char *);
extern const char *dlerror(void);
extern void exit(int);
extern void *malloc(unsigned);
extern int usleep(unsigned);

static char out[512];
#define fail(...) do { int n_ = snprintf(out, sizeof out, "condor-gl: " __VA_ARGS__); write(2, out, n_); exit(1); } while (0)

static void *lib(const char *name) {
	void *h = dlopen(name, 0);
	if (!h) fail("dlopen %s: %s\n", name, dlerror());
	return h;
}
static void *sym(void *h, const char *name) {
	void *p = dlsym(h, name);
	if (!p) fail("no %s\n", name);
	return p;
}
static int num(const char *s) {
	int n = 0;
	while (*s >= '0' && *s <= '9') n = n * 10 + (*s++ - '0');
	return n;
}
static void readAll(void *p, unsigned n) {
	char *b = p;
	while (n > 0) {
		long r = read(0, b, n);
		if (r <= 0) exit(0); /* condor-init has gone, or closed us */
		b += r; n -= r;
	}
}
#define say(...) do { int n_ = snprintf(out, sizeof out, __VA_ARGS__); write(1, out, n_); } while (0)
static void reply(char c) { write(1, &c, 1); }

/* yoffset: where the display shows the framebuffer from (struct fb_var_screeninfo, word 5). */
static int fbfd = -1;
static u32 yoffset(void) {
	u32 v[40];
	if (fbfd < 0 || ioctl(fbfd, 0x4600 /* FBIOGET_VSCREENINFO */, v) != 0) return 0;
	return v[5];
}

typedef struct { int kind; float x0, y0, x1, y1, u0, v0, u1, v1, a0, a1, mul, r, g, b, mix; } quad;
#define MAXQ 64

int main(int argc, char **argv) {
	if (argc < 4) fail("usage: glanim <shm file> <width> <height>\n");
	int W = num(argv[2]), H = num(argv[3]);
	unsigned size = (unsigned)W * H * 4;
	fbfd = open("/dev/graphics/fb0", 0);
	/* "glanim show W H": a test to watch. The screen as condor drew it (read from the
	   framebuffer) is shown through the GPU, moved down 300 pixels, for 6 seconds; then the
	   screen is red for 4 seconds. */
	int show = argv[1][0] == 's';
	unsigned char *mem;
	if (show) {
		mem = malloc(size * 3);
		if (!mem || fbfd < 0) fail("show: no memory or framebuffer\n");
		unsigned got = 0;
		while (got < size) { long r = read(fbfd, mem + got, size - got); if (r <= 0) break; got += r; }
		say("show: read %u bytes of the screen as condor drew it\n", got);
	} else {
		int shm = open(argv[1], 2 /* O_RDWR */);
		if (shm < 0) fail("can't open %s\n", argv[1]);
		mem = mmap(0, size * 3, 3 /* READ|WRITE */, 1 /* SHARED */, shm, 0);
		if (mem == (void *)-1) fail("mmap %s\n", argv[1]);
	}

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

	void (*glViewport)(int, int, int, int) = sym(gl, "glViewport");
	u32 (*glCreateShader)(u32) = sym(gl, "glCreateShader");
	void (*glShaderSource)(u32, int, const char **, const int *) = sym(gl, "glShaderSource");
	void (*glCompileShader)(u32) = sym(gl, "glCompileShader");
	void (*glGetShaderiv)(u32, u32, int *) = sym(gl, "glGetShaderiv");
	void (*glGetShaderInfoLog)(u32, int, int *, char *) = sym(gl, "glGetShaderInfoLog");
	u32 (*glCreateProgram)(void) = sym(gl, "glCreateProgram");
	void (*glAttachShader)(u32, u32) = sym(gl, "glAttachShader");
	void (*glBindAttribLocation)(u32, u32, const char *) = sym(gl, "glBindAttribLocation");
	void (*glLinkProgram)(u32) = sym(gl, "glLinkProgram");
	void (*glUseProgram)(u32) = sym(gl, "glUseProgram");
	int (*glGetUniformLocation)(u32, const char *) = sym(gl, "glGetUniformLocation");
	void (*glUniform1i)(int, int) = sym(gl, "glUniform1i");
	void (*glUniform1f)(int, float) = sym(gl, "glUniform1f");
	void (*glUniform2f)(int, float, float) = sym(gl, "glUniform2f");
	void (*glUniform3f)(int, float, float, float) = sym(gl, "glUniform3f");
	void (*glVertexAttribPointer)(u32, int, u32, unsigned char, int, const void *) = sym(gl, "glVertexAttribPointer");
	void (*glEnableVertexAttribArray)(u32) = sym(gl, "glEnableVertexAttribArray");
	void (*glDrawArrays)(u32, int, int) = sym(gl, "glDrawArrays");
	void (*glGenTextures)(int, u32 *) = sym(gl, "glGenTextures");
	void (*glBindTexture)(u32, u32) = sym(gl, "glBindTexture");
	void (*glTexParameteri)(u32, u32, int) = sym(gl, "glTexParameteri");
	void (*glTexImage2D)(u32, int, int, int, int, int, u32, u32, const void *) = sym(gl, "glTexImage2D");
	void (*glTexSubImage2D)(u32, int, int, int, int, int, u32, u32, const void *) = sym(gl, "glTexSubImage2D");
	void (*glEnable)(u32) = sym(gl, "glEnable");
	void (*glDisable)(u32) = sym(gl, "glDisable");
	void (*glBlendFunc)(u32, u32) = sym(gl, "glBlendFunc");
	void (*glClearColor)(float, float, float, float) = sym(gl, "glClearColor");
	void (*glClear)(u32) = sym(gl, "glClear");
	void (*glReadPixels)(int, int, int, int, u32, u32, void *) = sym(gl, "glReadPixels");
	const char *(*glGetString)(u32) = sym(gl, "glGetString");

	/* Window first, then the driver: the order the test showed working (and exiting cleanly). */
	EGLNativeWindowType win = createDisplaySurface();
	if (!win) fail("android_createDisplaySurface failed (is SurfaceFlinger still running?)\n");
	EGLDisplay dpy = eglGetDisplay(0);
	EGLint maj, min;
	if (!dpy || !eglInitialize(dpy, &maj, &min)) fail("eglInitialize 0x%x\n", eglGetError());
	const EGLint cfgAttr[] = {0x3033, 4, 0x3040, 4, 0x3024, 8, 0x3023, 8, 0x3022, 8, 0x3038};
	EGLConfig cfg; EGLint n = 0;
	if (!eglChooseConfig(dpy, cfgAttr, &cfg, 1, &n) || n < 1) fail("no EGL config 0x%x\n", eglGetError());
	EGLSurface surf = eglCreateWindowSurface(dpy, cfg, win, 0);
	if (!surf) fail("eglCreateWindowSurface 0x%x\n", eglGetError());
	const EGLint ctxAttr[] = {0x3098, 2, 0x3038};
	EGLContext ctx = eglCreateContext(dpy, cfg, 0, ctxAttr);
	if (!ctx || !eglMakeCurrent(dpy, surf, surf, ctx)) fail("context 0x%x\n", eglGetError());
	eglSwapInterval(dpy, 1);
	EGLint sw = 0, sh = 0;
	eglQuerySurface(dpy, surf, 0x3057, &sw);
	eglQuerySurface(dpy, surf, 0x3056, &sh);
	if (sw != W || sh != H) fail("the GPU's screen is %dx%d, not %dx%d\n", sw, sh, W, H);
	glViewport(0, 0, W, H);

	/* Images are uploaded as RGBA, so a texel's r g b are the framebuffer's B G R. */
	const char *vs =
		"attribute vec2 p; attribute vec2 t; attribute float a; uniform vec2 size;"
		"varying highp vec2 uv; varying float al;"
		"void main(){ uv = t / size; al = a;"
		" gl_Position = vec4(p.x / size.x * 2.0 - 1.0, 1.0 - p.y / size.y * 2.0, 0.0, 1.0); }";
	const char *fs =
		"precision highp float; uniform sampler2D img; uniform int solid; uniform float mul;"
		"uniform vec3 tint; uniform float mixk; varying highp vec2 uv; varying float al;"
		"void main(){ if (solid == 1) { gl_FragColor = vec4(tint, al); return; }"
		" vec3 c = texture2D(img, uv).bgr; gl_FragColor = vec4(mix(c, tint, mixk) * mul, 1.0); }";
	const char *src[2] = {vs, fs};
	u32 sh2[2];
	for (int i = 0; i < 2; i++) {
		sh2[i] = glCreateShader(i == 0 ? 0x8B31 : 0x8B30);
		glShaderSource(sh2[i], 1, &src[i], 0);
		glCompileShader(sh2[i]);
		int ok = 0;
		glGetShaderiv(sh2[i], 0x8B81, &ok);
		if (!ok) { char log[300]; int ln = 0; glGetShaderInfoLog(sh2[i], sizeof log - 1, &ln, log); log[ln] = 0; fail("shader: %s\n", log); }
	}
	u32 prog = glCreateProgram();
	glAttachShader(prog, sh2[0]); glAttachShader(prog, sh2[1]);
	glBindAttribLocation(prog, 0, "p");
	glBindAttribLocation(prog, 1, "t");
	glBindAttribLocation(prog, 2, "a");
	glLinkProgram(prog);
	glUseProgram(prog);
	glUniform2f(glGetUniformLocation(prog, "size"), W, H);
	glUniform1i(glGetUniformLocation(prog, "img"), 0);
	int uSolid = glGetUniformLocation(prog, "solid"), uMul = glGetUniformLocation(prog, "mul");
	int uTint = glGetUniformLocation(prog, "tint"), uMix = glGetUniformLocation(prog, "mixk");
	glBlendFunc(0x0302 /* SRC_ALPHA */, 0x0303 /* ONE_MINUS_SRC_ALPHA */);
	glEnableVertexAttribArray(0);
	glEnableVertexAttribArray(1);
	glEnableVertexAttribArray(2);

	u32 tex[3];
	int loaded[3] = {0, 0, 0};
	glGenTextures(3, tex);
	for (int i = 0; i < 3; i++) {
		glBindTexture(0x0DE1, tex[i]);
		glTexParameteri(0x0DE1, 0x2801, 0x2600); /* NEAREST: whole pixels, exact copies */
		glTexParameteri(0x0DE1, 0x2800, 0x2600);
		glTexParameteri(0x0DE1, 0x2802, 0x812F);
		glTexParameteri(0x0DE1, 0x2803, 0x812F);
	}
	glClearColor(0, 0, 0, 1);
	if (show) {
		say("show: %s on a %dx%d screen\n", glGetString(0x1F01), sw, sh);
		glBindTexture(0x0DE1, tex[0]);
		glTexImage2D(0x0DE1, 0, 0x1908, W, H, 0, 0x1908, 0x1401, mem);
		/* The whole picture, moved 300 native columns (down on the portrait screen). */
		float v4[4][5] = {{300, 0, 0, 0, 1}, {W + 300, 0, W, 0, 1}, {300, H, 0, H, 1}, {W + 300, H, W, H, 1}};
		glVertexAttribPointer(0, 2, 0x1406, 0, 20, &v4[0][0]);
		glVertexAttribPointer(1, 2, 0x1406, 0, 20, &v4[0][2]);
		glVertexAttribPointer(2, 1, 0x1406, 0, 20, &v4[0][4]);
		glUniform1i(uSolid, 0);
		glUniform1f(uMul, 1);
		glUniform1f(uMix, 0);
		for (int i = 0; i < 360; i++) {
			glClear(0x4000);
			glDrawArrays(5, 0, 4);
			if (i == 0) {
				/* What the GPU drew, read back: a pixel of the picture against the source. */
				unsigned char px[4];
				int x = W / 2 + 300, y = H / 2;
				glReadPixels(x, H - 1 - y, 1, 1, 0x1908, 0x1401, px);
				unsigned char *s = mem + 4 * (y * W + x - 300);
				say("show: GPU drew r%d g%d b%d where the picture has r%d g%d b%d\n",
					px[0], px[1], px[2], s[2], s[1], s[0]);
			}
			eglSwapBuffers(dpy, surf);
		}
		say("show: now red for 4 seconds\n");
		glClearColor(1, 0, 0, 1);
		for (int i = 0; i < 240; i++) { glClear(0x4000); eglSwapBuffers(dpy, surf); }
		say("show: done\n");
		exit(0);
	}
	reply('R');

	static quad q[MAXQ];
	float v[4][5];
	for (;;) {
		u32 hdr[2];
		readAll(hdr, sizeof hdr);
		if (hdr[0] == 1) {
			for (int i = 0; i < 3; i++) {
				if (!(hdr[1] & (1u << i))) continue;
				glBindTexture(0x0DE1, tex[i]);
				if (loaded[i])
					glTexSubImage2D(0x0DE1, 0, 0, 0, W, H, 0x1908, 0x1401, mem + i * size);
				else
					glTexImage2D(0x0DE1, 0, 0x1908, W, H, 0, 0x1908, 0x1401, mem + i * size);
				loaded[i] = 1;
			}
			reply('k');
			continue;
		}
		if (hdr[0] != 2 && hdr[0] != 3) fail("unknown command %u\n", hdr[0]);
		if (hdr[1] > MAXQ) fail("%u quads in a frame\n", hdr[1]);
		readAll(q, hdr[1] * sizeof(quad));
		/* The last frame is drawn until the display shows the framebuffer from its top. */
		for (int tries = 0; tries < (hdr[0] == 3 ? 3 : 1); tries++) {
			glClear(0x4000);
			for (u32 i = 0; i < hdr[1]; i++) {
				quad *k = &q[i];
				float c[4][4] = {{k->x0, k->y0, k->u0, k->v0}, {k->x1, k->y0, k->u1, k->v0},
				                 {k->x0, k->y1, k->u0, k->v1}, {k->x1, k->y1, k->u1, k->v1}};
				for (int j = 0; j < 4; j++) {
					v[j][0] = c[j][0]; v[j][1] = c[j][1]; v[j][2] = c[j][2]; v[j][3] = c[j][3];
					v[j][4] = j < 2 ? k->a0 : k->a1;
				}
				glVertexAttribPointer(0, 2, 0x1406, 0, 20, &v[0][0]);
				glVertexAttribPointer(1, 2, 0x1406, 0, 20, &v[0][2]);
				glVertexAttribPointer(2, 1, 0x1406, 0, 20, &v[0][4]);
				glUniform3f(uTint, k->r, k->g, k->b);
				if (k->kind == 3) {
					glUniform1i(uSolid, 1);
					glEnable(0x0BE2 /* BLEND */);
				} else {
					if (k->kind < 0 || k->kind > 2) fail("quad kind %d\n", k->kind);
					glUniform1i(uSolid, 0);
					glDisable(0x0BE2);
					glBindTexture(0x0DE1, tex[k->kind]);
					glUniform1f(uMul, k->mul);
					glUniform1f(uMix, k->mix);
				}
				glDrawArrays(5 /* TRIANGLE_STRIP */, 0, 4);
			}
			eglSwapBuffers(dpy, surf);
			if (hdr[0] != 3 || yoffset() == 0) break;
		}
		reply('k');
	}
}
