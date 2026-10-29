//go:build windows

package cirunner

// setUmask su Windows non fa nulla: i test che leggono i bit Unix si saltano.
func setUmask(int) func() { return func() {} }
