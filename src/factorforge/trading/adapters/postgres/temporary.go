package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/jackc/pgx/v5"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TemporaryServer is an explicitly created experiment database. The caller
// supplies native PostgreSQL binaries; no Python or running database is used.
type TemporaryServer struct {
	AdminDSN, SIMDSN, LIVEDSN string
	directory, socket, bin    string
}

func randomSecret() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", failure("TEST_DATABASE_UNAVAILABLE", 503)
	}
	return hex.EncodeToString(data), nil
}
func nativeCommand(ctx context.Context, bin string, args ...string) error {
	command := exec.CommandContext(ctx, bin, args...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if command.Run() != nil {
		return failure("TEST_DATABASE_UNAVAILABLE", 503)
	}
	return nil
}
func localDSN(user, password, socket string) string {
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(user, password), Path: "/postgres"}
	q := url.Values{"host": {socket}, "sslmode": {"disable"}}
	u.RawQuery = q.Encode()
	return u.String()
}
func StartTemporary(ctx context.Context, directory, bin string) (*TemporaryServer, error) {
	if runtime.GOOS != "linux" {
		return nil, failure("LINUX_ISOLATION_REQUIRED", 503)
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	data := filepath.Join(root, "pgdata")
	relative, err := filepath.Rel(root, data)
	if err != nil || strings.HasPrefix(relative, "..") {
		return nil, failure("TEST_DATABASE_SCOPE_INVALID", 503)
	}
	if _, err = os.Stat(data); !os.IsNotExist(err) {
		return nil, failure("TEST_DATABASE_ALREADY_EXISTS", 503)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	adminSecret, err := randomSecret()
	if err != nil {
		return nil, err
	}
	passwordFile := filepath.Join(root, "admin-password.tmp")
	if err = os.WriteFile(passwordFile, []byte(adminSecret), 0600); err != nil {
		return nil, err
	}
	defer os.Remove(passwordFile)
	if err = nativeCommand(ctx, filepath.Join(bin, "initdb"), "-D", data, "-U", "ff_admin", "-A", "scram-sha-256", "--pwfile", passwordFile, "--no-locale", "--encoding=UTF8"); err != nil {
		return nil, err
	}
	socket, err := os.MkdirTemp("", "ffpg-")
	if err != nil {
		return nil, err
	}
	server := &TemporaryServer{directory: root, socket: socket, bin: bin, AdminDSN: localDSN("ff_admin", adminSecret, socket)}
	if err = nativeCommand(ctx, filepath.Join(bin, "pg_ctl"), "-D", data, "-l", filepath.Join(root, "postgres.log"), "-w", "-o", "-F -c listen_addresses='' -k "+socket, "start"); err != nil {
		os.RemoveAll(socket)
		return nil, err
	}
	okay := false
	defer func() {
		if !okay {
			server.Close()
		}
	}()
	for _, environment := range []string{"SIM", "LIVE"} {
		if err = Initialize(ctx, server.AdminDSN, environment); err != nil {
			return nil, err
		}
	}
	connection, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		return nil, sqlError(err)
	}
	defer connection.Close(ctx)
	for _, environment := range []string{"sim", "live"} {
		secret, err := randomSecret()
		if err != nil {
			return nil, err
		}
		role := "ff_test_" + environment
		if _, err = connection.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD '"+secret+"'; GRANT factorforge_"+environment+" TO "+role); err != nil {
			return nil, sqlError(err)
		}
		dsn := localDSN(role, secret, socket)
		if environment == "sim" {
			server.SIMDSN = dsn
		} else {
			server.LIVEDSN = dsn
		}
	}
	okay = true
	return server, nil
}
func (s *TemporaryServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := nativeCommand(ctx, filepath.Join(s.bin, "pg_ctl"), "-D", filepath.Join(s.directory, "pgdata"), "-m", "immediate", "-w", "stop")
	os.RemoveAll(s.socket)
	return err
}
