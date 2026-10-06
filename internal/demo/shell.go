package demo

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/exec"
)

// Shell simulé du mode démo (commandes du prototype), servi par le même
// WebSocket que le vrai terminal. Il gère la saisie comme un TTY : écho,
// effacement, Ctrl+C, Ctrl+D, séquences d'échappement ignorées.

var _ exec.Backend = (*Sim)(nil)

const prompt = "/app $ "

func (s *Sim) Exec(ctx context.Context, _ access.User, ns, pod, container string, cmd []string, st exec.Streams) error {
	s.mu.Lock()
	p := s.findPod(ns, pod)
	if p == nil {
		s.mu.Unlock()
		return notFound("pods", pod)
	}
	d := p.wl.def
	status, ip, node, created := p.pod.DisplayStatus, p.pod.PodIP, p.pod.NodeName, p.pod.CreatedAt
	s.mu.Unlock()

	if container != "" && container != d.Name {
		return apierrors.NewBadRequest(fmt.Sprintf("container %s is not valid for pod %s", container, pod))
	}
	if status != "Running" {
		return apierrors.NewBadRequest(fmt.Sprintf("unable to upgrade connection: container %s not running (%s)", d.Name, status))
	}
	if len(cmd) == 0 || cmd[0] != "/bin/sh" {
		return fmt.Errorf(`exec: %q: stat %s: no such file or directory`, cmd[0], cmd[0]) // images Alpine : pas de bash
	}
	if st.Resize != nil {
		go func() {
			for range st.Resize {
			}
		}()
	}

	sh := &demoShell{pod: pod, d: d, ip: ip, node: node, uptime: int(s.now.Sub(created).Seconds()), crash: d.Crashy}
	write := func(text string) error { _, err := io.WriteString(st.Stdout, text); return err }
	if err := write(prompt); err != nil {
		return err
	}
	buf := make([]byte, 256)
	var line []byte
	escape := 0 // octets restants d'une séquence d'échappement
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := st.Stdin.Read(buf)
		if err != nil {
			return nil
		}
		for _, b := range buf[:n] {
			switch {
			case escape > 0:
				escape--
				if escape == 1 && b != '[' && b != 'O' {
					escape = 0
				}
			case b == 0x1b:
				escape = 2 // ESC [ X : flèches, non gérées par ce shell
			case b == '\r' || b == '\n':
				_ = write("\r\n")
				out, exit, code := sh.run(strings.TrimSpace(string(line)))
				line = line[:0]
				if exit {
					if code != 0 {
						return &exec.ExitError{Code: code}
					}
					return nil
				}
				if out != "" {
					_ = write(strings.ReplaceAll(out, "\n", "\r\n") + "\r\n")
				}
				_ = write(prompt)
			case b == 0x7f || b == 0x08:
				if len(line) > 0 {
					line = line[:len(line)-1]
					_ = write("\b \b")
				}
			case b == 0x03: // Ctrl+C
				line = line[:0]
				_ = write("^C\r\n" + prompt)
			case b == 0x04: // Ctrl+D
				if len(line) == 0 {
					_ = write("exit\r\n")
					return nil
				}
			case b == '\t':
			case b >= 0x20:
				line = append(line, b)
				_ = write(string(b))
			}
		}
	}
}

type demoShell struct {
	pod, ip, node string
	d             workloadDef
	uptime        int
	crash         bool
}

func (sh *demoShell) run(line string) (out string, exit bool, code int) {
	args := strings.Fields(line)
	if len(args) == 0 {
		return "", false, 0
	}
	files := map[string]string{
		"api-gateway": "app  config  server.js  package.json  node_modules", "frontend": "etc  usr  var  docker-entrypoint.sh",
		"payment-worker": "payment-worker  config.yaml  migrations", "ml-inference": "model  server.py  requirements.txt  weights.safetensors",
		"prometheus": "prometheus  prometheus.yml  data", "coredns": "Corefile",
	}
	switch args[0] {
	case "exit":
		if len(args) > 1 {
			code, _ = strconv.Atoi(args[1])
		}
		return "", true, code
	case "help":
		return "Commandes simulées : ls, pwd, whoami, hostname, env, ps, df -h, cat /etc/os-release, curl localhost:8080/healthz, echo, date, clear, exit", false, 0
	case "ls":
		if f, ok := files[sh.d.Name]; ok {
			return f, false, 0
		}
		return "app", false, 0
	case "pwd":
		return "/app", false, 0
	case "whoami":
		return "app", false, 0
	case "hostname":
		return sh.pod, false, 0
	case "echo":
		return strings.Join(args[1:], " "), false, 0
	case "date":
		return "Tue Oct  6 09:00:00 UTC 2026", false, 0
	case "env":
		return strings.Join([]string{"HOSTNAME=" + sh.pod, "POD_NAMESPACE=" + sh.d.NS, "POD_IP=" + sh.ip, "PORT=8080",
			"KUBERNETES_SERVICE_HOST=10.0.0.1", "KUBERNETES_SERVICE_PORT=443", "NODE_NAME=" + sh.node}, "\n"), false, 0
	case "ps":
		return fmt.Sprintf("PID   USER     TIME  COMMAND\n    1 app       %d:%02d /app/%s\n   41 app       0:00 /bin/sh\n   48 app       0:00 ps",
			sh.uptime/60%60, sh.uptime%60, sh.d.Name), false, 0
	case "df":
		return "Filesystem     Size  Used Avail Use% Mounted on\noverlay         95G   38G   57G  40% /\ntmpfs           64M     0   64M   0% /dev", false, 0
	case "cat":
		if len(args) > 1 && args[1] == "/etc/os-release" {
			return "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.20.3\nPRETTY_NAME=\"Alpine Linux v3.20\"", false, 0
		}
		return fmt.Sprintf("cat: can't open '%s': No such file or directory", strings.Join(args[1:], " ")), false, 0
	case "curl":
		if sh.crash {
			return "curl: (7) Failed to connect to localhost port 8080: Connection refused", false, 0
		}
		return fmt.Sprintf(`{"status":"ok","pod":"%s","uptime_s":%d}`, sh.pod, sh.uptime), false, 0
	case "clear":
		return "\x1b[2J\x1b[H", false, 0
	}
	return fmt.Sprintf("/bin/sh: %s: not found", args[0]), false, 0
}
