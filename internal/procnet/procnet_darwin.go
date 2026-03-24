package procnet

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <arpa/inet.h>
#include <stdlib.h>
#include <string.h>

// Wrapper: list all PIDs into caller-provided buffer
static int list_all_pids(pid_t *buf, int bufsize) {
    return proc_listallpids(buf, bufsize * (int)sizeof(pid_t));
}

// Wrapper: get process name
static int get_proc_name(pid_t pid, char *buf, int bufsize) {
    return proc_name(pid, buf, bufsize);
}

struct port_entry {
    unsigned short local_port;
};

// Collect all TCP local ports for a given PID.
static int collect_tcp_ports(pid_t pid, struct port_entry *out, int max_entries) {
    int count = 0;
    int buf_size = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, NULL, 0);
    if (buf_size <= 0) return 0;

    struct proc_fdinfo *fds = (struct proc_fdinfo *)malloc(buf_size);
    if (!fds) return 0;

    int actual = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, fds, buf_size);
    if (actual <= 0) { free(fds); return 0; }

    int num_fds = actual / (int)sizeof(struct proc_fdinfo);
    for (int i = 0; i < num_fds && count < max_entries; i++) {
        if (fds[i].proc_fdtype != PROX_FDTYPE_SOCKET) continue;

        struct socket_fdinfo si;
        int ret = proc_pidfdinfo(pid, fds[i].proc_fd, PROC_PIDFDSOCKETINFO, &si, sizeof(si));
        if (ret <= 0) continue;
        if (si.psi.soi_kind != SOCKINFO_TCP) continue;

        int lport = si.psi.soi_proto.pri_tcp.tcpsi_ini.insi_lport;
        unsigned short port = (unsigned short)ntohs((uint16_t)lport);
        if (port == 0) continue;

        out[count].local_port = port;
        count++;
    }
    free(fds);
    return count;
}
*/
import "C"

import (
	"sync"
	"time"
)

// Mapper maintains a mapping of local TCP ports to process names.
type Mapper struct {
	mu       sync.RWMutex
	byPort   map[uint16]string
	interval time.Duration
}

func NewMapper() *Mapper {
	return &Mapper{
		byPort:   make(map[uint16]string),
		interval: 3 * time.Second,
	}
}

// ProcessForPort returns the process name for a local TCP port.
func (m *Mapper) ProcessForPort(port uint16) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byPort[port]
}

// Refresh rebuilds the port -> process mapping.
func (m *Mapper) Refresh() {
	mapping := buildMapping()
	m.mu.Lock()
	m.byPort = mapping
	m.mu.Unlock()
}

// StartRefresh periodically refreshes the mapping.
func (m *Mapper) StartRefresh(done <-chan struct{}) {
	m.Refresh()
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.Refresh()
		case <-done:
			return
		}
	}
}

func buildMapping() map[uint16]string {
	result := make(map[uint16]string)

	// Get PID count
	pidCount := C.list_all_pids(nil, 0)
	if pidCount <= 0 {
		pidCount = 4096
	}

	pids := make([]C.pid_t, int(pidCount)+100)
	count := int(C.list_all_pids(&pids[0], C.int(len(pids))))
	if count <= 0 {
		return result
	}

	var nameBuf [256]C.char
	var entries [64]C.struct_port_entry

	for i := 0; i < count; i++ {
		pid := pids[i]
		if pid == 0 {
			continue
		}

		ret := C.get_proc_name(pid, &nameBuf[0], 256)
		if ret <= 0 {
			continue
		}
		procName := C.GoString(&nameBuf[0])

		n := int(C.collect_tcp_ports(pid, &entries[0], 64))
		for j := 0; j < n; j++ {
			port := uint16(entries[j].local_port)
			result[port] = procName
		}
	}

	return result
}
