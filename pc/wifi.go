package main

// Turns on "Sync with this iPhone over Wi-Fi" without iTunes. That checkbox is
// just the lockdown value com.apple.mobile.wireless_lockdown/EnableWifiConnections
// on the phone, which we set over USB using the PC's existing pairing record.

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"
)

const lockdownPort = 62078

type pairRecord struct {
	HostID, SystemBUID string
	Cert               tls.Certificate
}

func readPairRecord(udid string) (pairRecord, error) {
	c, err := dialMux()
	if err != nil {
		return pairRecord{}, err
	}
	defer c.Close()
	resp, err := muxRequest(c, "ReadPairRecord", `<key>PairRecordID</key><string>`+udid+`</string>`)
	if err != nil {
		return pairRecord{}, err
	}
	raw, err := plistData(resp, "PairRecordData")
	if err != nil {
		return pairRecord{}, fmt.Errorf("no pairing record for this iPhone - plug it in and tap Trust")
	}
	rec := string(raw)
	if strings.HasPrefix(rec, "bplist") {
		return pairRecord{}, fmt.Errorf("pairing record is in binary format, which ipakill can't read")
	}
	certPEM, err1 := plistData(rec, "HostCertificate")
	keyPEM, err2 := plistData(rec, "HostPrivateKey")
	if err1 != nil || err2 != nil {
		return pairRecord{}, fmt.Errorf("pairing record is missing the host certificate")
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return pairRecord{}, fmt.Errorf("bad pairing certificate: %v", err)
	}
	return pairRecord{HostID: plistString(rec, "HostID"), SystemBUID: plistString(rec, "SystemBUID"), Cert: cert}, nil
}

func plistData(s, key string) ([]byte, error) {
	m := regexp.MustCompile(`<key>` + key + `</key>\s*<data>([^<]*)</data>`).FindStringSubmatch(s)
	if m == nil {
		return nil, fmt.Errorf("%s not found", key)
	}
	return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(m[1]), ""))
}

// lockdown frames plist messages as a 4-byte big-endian length + XML.
func lockdownCall(c net.Conn, fields string) (string, error) {
	c.SetDeadline(time.Now().Add(10 * time.Second))
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>` +
		`<key>Label</key><string>ipakill</string>` + fields + `</dict></plist>`)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(body)))
	if _, err := c.Write(append(n[:], body...)); err != nil {
		return "", err
	}
	if _, err := io.ReadFull(c, n[:]); err != nil {
		return "", err
	}
	resp := make([]byte, binary.BigEndian.Uint32(n[:]))
	if _, err := io.ReadFull(c, resp); err != nil {
		return "", err
	}
	if e := plistString(string(resp), "Error"); e != "" {
		return "", fmt.Errorf("iPhone said: %s", e)
	}
	return string(resp), nil
}

// setWifiSync turns Wi-Fi sync on or off. Pass on=nil to only read the current state.
func setWifiSync(on *bool) (bool, error) {
	devs, err := listDevices()
	if err != nil {
		return false, err
	}
	var dev *Device
	for i := range devs {
		if devs[i].Conn == "USB" {
			dev = &devs[i]
			break
		}
	}
	if dev == nil {
		return false, fmt.Errorf("plug the iPhone in by USB for this one-time step")
	}
	pr, err := readPairRecord(dev.UDID)
	if err != nil {
		return false, err
	}

	c, err := dialMux()
	if err != nil {
		return false, err
	}
	defer c.Close()
	// usbmuxd wants the port in network byte order.
	port := (lockdownPort>>8)&0xff | (lockdownPort&0xff)<<8
	resp, err := muxRequest(c, "Connect", fmt.Sprintf(
		`<key>DeviceID</key><integer>%d</integer><key>PortNumber</key><integer>%d</integer>`, dev.ID, port))
	if err != nil {
		return false, err
	}
	if !strings.Contains(resp, "<key>Number</key><integer>0</integer>") {
		return false, fmt.Errorf("could not reach the iPhone's lockdown service")
	}

	if _, err := lockdownCall(c, `<key>Request</key><string>QueryType</string>`); err != nil {
		return false, err
	}
	resp, err = lockdownCall(c, `<key>Request</key><string>StartSession</string>`+
		`<key>HostID</key><string>`+pr.HostID+`</string>`+
		`<key>SystemBUID</key><string>`+pr.SystemBUID+`</string>`)
	if err != nil {
		return false, err
	}
	var conn net.Conn = c
	if strings.Contains(resp, "<key>EnableSessionSSL</key><true/>") {
		t := tls.Client(c, &tls.Config{
			Certificates:       []tls.Certificate{pr.Cert},
			InsecureSkipVerify: true, // the phone uses a self-signed cert from pairing
			MinVersion:         tls.VersionTLS12,
		})
		if err := t.Handshake(); err != nil {
			return false, fmt.Errorf("secure session failed: %v", err)
		}
		conn = t
	}

	const domain = `<key>Domain</key><string>com.apple.mobile.wireless_lockdown</string>` +
		`<key>Key</key><string>EnableWifiConnections</string>`
	if on != nil {
		val := "<false/>"
		if *on {
			val = "<true/>"
		}
		if _, err := lockdownCall(conn, `<key>Request</key><string>SetValue</string>`+domain+`<key>Value</key>`+val); err != nil {
			return false, err
		}
	}
	resp, err = lockdownCall(conn, `<key>Request</key><string>GetValue</string>`+domain)
	if err != nil {
		return false, err
	}
	return strings.Contains(resp, "<key>Value</key><true/>"), nil
}
