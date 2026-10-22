//go:build windows

package client

import "errors"

var errOpencodeWindows = errors.New("OpenCode connections are not available on Windows yet")

var errOpencodeEndpointGone = errOpencodeWindows

func captureOpencodeEndpoint(string) (opencodeEndpoint, error) {
	return opencodeEndpoint{}, errOpencodeWindows
}

func validateOpencodeEndpoint(opencodeEndpoint) error { return errOpencodeWindows }

func opencodeRequest(opencodeEndpoint, any, any) error { return errOpencodeWindows }

func opencodeHelloAt(opencodeEndpoint) (opencodeHello, error) {
	return opencodeHello{}, errOpencodeWindows
}

func discoverOpencodeEndpoint(string) (opencodeEndpoint, error) {
	return opencodeEndpoint{}, errOpencodeWindows
}
