package focus

import (
	"bufio"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Socket is a listening unix socket: the process serving it and the processes connected to it.
type Socket struct {
	Server  int
	Clients []int
}

// ParseLsof maps unix socket paths to their server and the PIDs of the processes connected to
// them, from the output of `lsof -U -F pfdn`. A server's socket fds carry the path; a client's
// connection points ("->0x…") at the address of one of those fds.
func ParseLsof(out string) map[string]Socket {
	type fd struct{ dev, name string }
	fds := map[int][]fd{}
	pid := 0
	var cur fd
	flush := func() {
		if pid != 0 && (cur.dev != "" || cur.name != "") {
			fds[pid] = append(fds[pid], cur)
		}
		cur = fd{}
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		v := line[1:]
		switch line[0] {
		case 'p':
			flush()
			pid, _ = strconv.Atoi(v)
		case 'f':
			flush()
		case 'd':
			cur.dev = v
		case 'n':
			cur.name = v
		}
	}
	flush()

	pathOf := map[string]string{} // socket address → path
	socks := map[string]Socket{}
	for p, list := range fds {
		for _, f := range list {
			if strings.HasPrefix(f.name, "/") {
				pathOf[f.dev] = f.name
				socks[f.name] = Socket{Server: p}
			}
		}
	}
	for p, list := range fds {
		for _, f := range list {
			peer, ok := strings.CutPrefix(f.name, "->")
			if !ok {
				continue
			}
			if path, ok := pathOf[peer]; ok && !slices.Contains(socks[path].Clients, p) {
				sk := socks[path]
				sk.Clients = append(sk.Clients, p)
				socks[path] = sk
			}
		}
	}
	return socks
}

// ParseSS maps unix socket paths to their server and the PIDs of processes named command connected
// to them, from the output of `ss -xpH`. The server side of a connection shows the path and its
// inode; the client side shows that inode as its peer.
func ParseSS(out, command string) map[string]Socket {
	type end struct {
		path        string
		inode, peer string
		pids        []int
	}
	var ends []end
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || !strings.HasPrefix(f[0], "u_") {
			continue
		}
		e := end{path: f[4], inode: f[5], peer: f[7]}
		for _, users := range f[8:] {
			for _, u := range strings.Split(users, "),(") {
				if !strings.Contains(u, `"`+command+`"`) {
					continue
				}
				if _, v, ok := strings.Cut(u, "pid="); ok {
					v, _, _ = strings.Cut(v, ",")
					if pid, err := strconv.Atoi(v); err == nil {
						e.pids = append(e.pids, pid)
					}
				}
			}
		}
		ends = append(ends, e)
	}
	pathOf := map[string]string{}
	socks := map[string]Socket{}
	for _, e := range ends {
		if strings.HasPrefix(e.path, "/") {
			pathOf[e.inode] = e.path
			sk := socks[e.path]
			if sk.Server == 0 && len(e.pids) > 0 {
				sk.Server = e.pids[0]
			}
			socks[e.path] = sk
		}
	}
	for _, e := range ends {
		path, ok := pathOf[e.peer]
		if !ok || e.path != "*" {
			continue
		}
		sk := socks[path]
		for _, pid := range e.pids {
			if !slices.Contains(sk.Clients, pid) {
				sk.Clients = append(sk.Clients, pid)
			}
		}
		socks[path] = sk
	}
	return socks
}

// socketClients lists the processes named command that are connected to a unix socket that
// matches (by its path and the PID of its server), lowest PID first.
func (f *Focuser) socketClients(command string, match func(path string, server int) bool) []int {
	var clients map[string]Socket
	if f.goos() == "darwin" {
		out, err := f.Run("lsof", "-U", "-a", "-c", command, "-F", "pfdn")
		if err != nil && len(out) == 0 {
			log.Printf("focus: lsof -c %s: %v", command, err)
		}
		clients = ParseLsof(string(out))
	} else {
		out, err := f.Run("ss", "-xpH")
		if err != nil {
			log.Printf("focus: ss: %v", err)
		}
		clients = ParseSS(string(out), command)
	}
	var pids []int
	for path, sk := range clients {
		if match(path, sk.Server) {
			pids = append(pids, sk.Clients...)
		}
	}
	slices.Sort(pids)
	return slices.Compact(pids)
}

// lookBinary finds a binary; launchd agents run with a minimal PATH.
func lookBinary(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin",
		filepath.Join(home, ".cargo", "bin"), filepath.Join(home, ".local", "bin")} {
		if p := filepath.Join(dir, name); fileExists(p) {
			return p
		}
	}
	return name
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
