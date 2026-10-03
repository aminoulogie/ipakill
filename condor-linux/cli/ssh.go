package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// condor ssh: log in to the tablet's Alpine over Wi-Fi, no cable needed after setup.
//
//	condor ssh setup   install openssh on the tablet, put this PC's key in root's
//	                   authorized_keys, start sshd (condor-init starts it at every boot)
//	condor ssh         connect (finds the tablet's Wi-Fi address over USB)
//
// Root logs in with keys only: Alpine's sshd defaults to PermitRootLogin prohibit-password.
func sshCmd(args []string) error {
	if len(args) > 0 && args[0] == "setup" {
		return sshSetup()
	}
	ip, err := tabletIP()
	if err != nil {
		return err
	}
	client, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("no ssh client on this PC (Windows: Settings > Apps > Optional features > OpenSSH Client)")
	}
	c := exec.Command(client, append([]string{"root@" + ip}, args...)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

var reIfconfigIP = regexp.MustCompile(`\bip (\d+\.\d+\.\d+\.\d+)`)

// tabletIP reads wlan0's address over USB (Android's ifconfig: "wlan0: ip 192.168.1.5 mask ...").
func tabletIP() (string, error) {
	if err := needDevice(); err != nil {
		return "", fmt.Errorf("%v\n(without USB, use: ssh root@<tablet address>; 'wifi status' on the tablet shows it)", err)
	}
	m := reIfconfigIP.FindStringSubmatch(sh("ifconfig wlan0"))
	if m == nil || m[1] == "0.0.0.0" {
		return "", fmt.Errorf("the tablet has no Wi-Fi address yet (on the tablet: wifi status)")
	}
	return m[1], nil
}

var reAlpineExit = regexp.MustCompile(`alpine-run: exit (\d+)\s*$`)

// alpineRun runs a shell command inside the tablet's Alpine and prints its output. The command
// is double-quoted for the tablet's outer shell (so ; > || $ reach Alpine's shell intact), and
// success is read from condor-init's "alpine-run: exit N" line, since adb drops exit codes.
func alpineRun(cmdline string) error {
	if strings.Contains(sh("su -c 'ls "+initPath+"'"), "No such") {
		return fmt.Errorf("%s is missing; install condor-init first (dev.cmd)", initPath)
	}
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(cmdline)
	out, _ := adb("shell", "su -c '"+initPath+" alpine-run \""+q+"\"'")
	out = strings.TrimRight(out, "\n")
	m := reAlpineExit.FindStringSubmatch(out)
	if body := strings.TrimSpace(reAlpineExit.ReplaceAllString(out, "")); body != "" {
		fmt.Println(body)
	}
	switch {
	case m == nil:
		return fmt.Errorf("no result from the tablet (is condor-init up to date? run dev.cmd)")
	case m[1] != "0":
		return fmt.Errorf("command failed on the tablet (exit %s): %s", m[1], cmdline)
	}
	return nil
}

func sshSetup() error {
	if err := needDevice(); err != nil {
		return err
	}
	if !strings.Contains(sh("su -c id"), "uid=0") {
		return fmt.Errorf("su doesn't give root on the tablet")
	}
	if v := sh("su -c 'cat " + alpineRoot + "/etc/alpine-release'"); v == "" || strings.Contains(v, "No such") {
		return fmt.Errorf("Alpine isn't installed (condor alpine install)")
	}

	fmt.Println("==> installing openssh on the tablet")
	if err := alpineRun("apk add openssh"); err != nil {
		return fmt.Errorf("apk add openssh failed (is the tablet online? wifi status, or run condor net): %w", err)
	}

	pub, err := pcPublicKey()
	if err != nil {
		return err
	}
	fmt.Println("==> authorizing this PC's key:", strings.Fields(pub)[0], "...", lastField(pub))
	keys := alpineRoot + "/root/.ssh/authorized_keys"
	existing := sh("su -c 'cat " + keys + "'")
	if strings.Contains(existing, "No such") {
		existing = ""
	}
	if !strings.Contains(existing, strings.Fields(pub)[1]) {
		merged := strings.TrimRight(existing, "\n")
		if merged != "" {
			merged += "\n"
		}
		sh("su -c 'mkdir " + alpineRoot + "/root/.ssh'")
		if err := writeRoot(keys, merged+pub+"\n"); err != nil {
			return err
		}
	}
	sh("su -c 'chmod 700 " + alpineRoot + "/root/.ssh; chmod 600 " + keys + "'")

	fmt.Println("==> starting sshd")
	if err := alpineRun("ssh-keygen -A >/dev/null 2>&1; pgrep -x sshd >/dev/null || /usr/sbin/sshd"); err != nil {
		return fmt.Errorf("starting sshd: %w", err)
	}
	if ip, err := tabletIP(); err == nil {
		fmt.Printf("\nReady. Connect over Wi-Fi with:  condor ssh   (or: ssh root@%s)\n", ip)
	} else {
		fmt.Println("\nReady. Connect with:  ssh root@<tablet address>  (wifi status on the tablet shows it)")
	}
	return nil
}

func lastField(s string) string {
	f := strings.Fields(s)
	return f[len(f)-1]
}

// pcPublicKey returns this PC's SSH public key, creating an ed25519 key if there's none.
func pcPublicKey() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(h, ".ssh")
	for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil && len(strings.Fields(string(b))) >= 2 {
			return strings.TrimSpace(string(b)), nil
		}
	}
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return "", fmt.Errorf("no SSH key and no ssh-keygen on this PC (Windows: Settings > Apps > Optional features > OpenSSH Client)")
	}
	os.MkdirAll(dir, 0o700)
	key := filepath.Join(dir, "id_ed25519")
	fmt.Println("==> creating an SSH key for this PC:", key)
	c := exec.Command(keygen, "-t", "ed25519", "-N", "", "-C", "condor-pc", "-f", key)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("ssh-keygen: %w", err)
	}
	b, err := os.ReadFile(key + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
