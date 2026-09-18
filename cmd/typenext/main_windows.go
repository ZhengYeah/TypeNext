//go:build windows && amd64

package main

import (
	"fmt"
	"typenext/internal/win"
)

func main() {
	defer func() {
		if p := recover(); p != nil {
			win.ShowFatal(fmt.Errorf("TypeNext stopped unexpectedly: %v", p))
		}
	}()
	close, ok := win.SingleInstance()
	if !ok {
		win.ShowFatal(fmt.Errorf("TypeNext is already running. Open it from the system tray."))
		return
	}
	defer close()
	if err := win.Run(); err != nil {
		win.ShowFatal(err)
	}
}
