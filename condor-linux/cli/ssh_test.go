package main

import "testing"

func TestIfconfigIP(t *testing.T) {
	out := "wlan0: ip 192.168.100.16 mask 255.255.255.0 flags [up broadcast running multicast]"
	m := reIfconfigIP.FindStringSubmatch(out)
	if m == nil || m[1] != "192.168.100.16" {
		t.Fatalf("got %v", m)
	}
	if reIfconfigIP.FindStringSubmatch("wlan0: ip 0.0.0.0 mask 0.0.0.0 flags [down]")[1] != "0.0.0.0" {
		t.Fatal("down interface should parse as 0.0.0.0")
	}
}
