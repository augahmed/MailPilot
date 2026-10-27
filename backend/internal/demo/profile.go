// Package demo owns only the fixed synthetic profile, fixtures and local preview
// adapter. It imports no credential stores, HTTP clients or inference runtime.
package demo

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"mailpilot.local/core/internal/auth"
	"mailpilot.local/core/internal/domain"
)

const AccountID domain.AccountID = "mailpilot-synthetic-demo-v1"
const markerName = "synthetic-demo-v1"
const marker = "MailPilot synthetic profile v1\n"

// Called before SQLite is opened. Existing unmarked directories are never
// adopted as demos; ordinary startup refuses a marked profile, including aliases.
func PrepareProfile(dir string, demo bool) error {
	path := filepath.Join(dir, markerName)
	if !demo {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return errors.New("synthetic profile requires demo startup")
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	err := os.Mkdir(dir, 0700)
	if err == nil {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = f.WriteString(marker)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid synthetic profile directory")
	}
	info, err = os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(marker)) {
		return errors.New("unrecognized synthetic profile")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != marker {
		return errors.New("unrecognized synthetic profile")
	}
	return nil
}

type Session struct{ Context context.Context }

func (s *Session) Status() auth.Status {
	return auth.Status{State: "connected", Demo: true, AccountID: AccountID, Email: "pilot@example.test"}
}
func (s *Session) blocked() auth.Status    { r := s.Status(); r.Code = "demo_mode"; return r }
func (s *Session) Begin() auth.BeginResult { return auth.BeginResult{Status: s.blocked()} }
func (s *Session) Check() auth.Status      { return s.blocked() }
func (s *Session) Disconnect() auth.Status { return s.blocked() }
func (s *Session) Cancel() auth.Status     { return s.blocked() }
func (s *Session) Session() (domain.AccountID, context.Context, error) {
	return AccountID, s.Context, s.Context.Err()
}
