//go:build !unix

package main

import "errors"

func lockStateDirectory(string) (func(), error) {
	return nil, errors.New("vm2api bridge requires a Unix runtime host")
}
