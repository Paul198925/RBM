package proc

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func pidAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	// a zombie still answers signal 0 until it is reaped
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z"
}
