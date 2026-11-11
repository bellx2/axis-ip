package main

/*
#include <stdint.h>
#include <stdlib.h>
#include "mdns_darwin.h"
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"time"
	"unsafe"
)

// browseState collects the results of the C callbacks.
type browseState struct {
	instances map[string]*instance // keyed by full service name
	hosts     map[string][]string  // hostname -> IPv4 addresses
}

type instance struct {
	name    string
	ifIndex uint32
	host    string
	port    int
	mac     string
}

// browseRaw browses regtype for the given duration and returns the raw results.
func browseRaw(regtype string, timeout time.Duration) (*browseState, error) {
	st := &browseState{instances: map[string]*instance{}, hosts: map[string][]string{}}
	h := cgo.NewHandle(st)
	defer h.Delete()

	cs := C.CString(regtype)
	defer C.free(unsafe.Pointer(cs))

	if rc := C.axBrowse(cs, C.int(timeout.Milliseconds()), C.uintptr_t(h)); rc != 0 {
		return st, fmt.Errorf("DNS-SD error %d", int(rc))
	}
	return st, nil
}

//export goBrowsed
func goBrowsed(h C.uintptr_t, name, full *C.char, ifIndex C.uint32_t) {
	st := cgo.Handle(h).Value().(*browseState)
	f := C.GoString(full)
	if _, ok := st.instances[f]; !ok {
		st.instances[f] = &instance{name: C.GoString(name), ifIndex: uint32(ifIndex)}
	}
}

//export goResolved
func goResolved(h C.uintptr_t, full, host *C.char, port C.uint16_t, mac *C.char) {
	st := cgo.Handle(h).Value().(*browseState)
	in, ok := st.instances[C.GoString(full)]
	if !ok {
		return
	}
	in.host = C.GoString(host)
	in.port = int(port)
	in.mac = C.GoString(mac)
}

//export goAddr
func goAddr(h C.uintptr_t, host, addr *C.char) {
	st := cgo.Handle(h).Value().(*browseState)
	hn, a := C.GoString(host), C.GoString(addr)
	for _, x := range st.hosts[hn] {
		if x == a {
			return
		}
	}
	st.hosts[hn] = append(st.hosts[hn], a)
}
