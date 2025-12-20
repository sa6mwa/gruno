package main

import _ "unsafe"

//go:linkname pslogExitProcess pkt.systems/pslog.exitProcess
var pslogExitProcess func()

func init() {
	pslogExitProcess = func() {}
}
