package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubnetGuard(t *testing.T) {
	_, allowed, _ := net.ParseCIDR("10.10.10.0/24")
	guard := subnetGuard(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		allowed,
	)

	cases := []struct {
		remoteAddr string
		want       int
	}{
		{"127.0.0.1:5001", http.StatusOK},        // loopback always allowed
		{"[::1]:5001", http.StatusOK},             // IPv6 loopback allowed
		{"10.10.10.100:5001", http.StatusOK},      // this device
		{"10.10.10.1:5001", http.StatusOK},        // gateway, in subnet
		{"10.10.10.255:5001", http.StatusOK},      // in subnet
		{"10.10.11.5:5001", http.StatusForbidden}, // adjacent subnet — denied
		{"192.168.1.5:5001", http.StatusForbidden},// different network — denied
		{"8.8.8.8:5001", http.StatusForbidden},    // public — denied
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/meta", nil)
		req.RemoteAddr = c.remoteAddr
		guard.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("remote %s: got %d, want %d", c.remoteAddr, rec.Code, c.want)
		}
	}
}

func TestLanSubnetUsesInterfaceMask(t *testing.T) {
	// lanSubnet should return a network (masked) address, not the host address.
	sub := lanSubnet()
	if sub == nil {
		t.Skip("no LAN interface detected in this environment")
	}
	if !sub.IP.Equal(sub.IP.Mask(sub.Mask)) {
		t.Errorf("lanSubnet returned unmasked network %s", sub.String())
	}
}
