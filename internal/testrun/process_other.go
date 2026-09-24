//go:build !darwin && !linux

package testrun

import "os/exec"

func configureProcessGroup(cmd *exec.Cmd) {}
func cleanupProcessGroup(cmd *exec.Cmd)   {}
