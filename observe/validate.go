package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"
)

var now = time.Now

func missing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}
func unchangedStamp(info os.FileInfo, s *Stamp) bool {
	if info == nil || !info.Mode().IsRegular() || s == nil {
		return false
	}
	size, _ := strconv.ParseInt(s.Size, 10, 64)
	modified, _ := strconv.ParseInt(s.MtimeNs, 10, 64)
	observed, _ := strconv.ParseInt(s.ObservedAtNs, 10, 64)
	return info.Size() == size && info.ModTime().UnixNano() == modified && modified <= observed-int64(2*time.Second)
}
func makeStamp(info os.FileInfo, observed time.Time) *Stamp {
	return &Stamp{strconv.FormatInt(info.Size(), 10), strconv.FormatInt(info.ModTime().UnixNano(), 10), strconv.FormatInt(observed.UnixNano(), 10)}
}
func sameInfo(a, b os.FileInfo) bool {
	return a != nil && b != nil && a.Mode().IsRegular() && b.Mode().IsRegular() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// Changed checks each captured result. Full always hashes content; Quick may
// use a previously certified hint outside the two-second racy window.
func (s Set) Changed(ctx context.Context, mode Mode) (*Record, error) {
	if mode != Full && mode != Quick {
		return nil, fmt.Errorf("unknown observation mode %d", mode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.data == nil {
		return nil, nil
	}
	for i, r := range s.data.records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matches, err := s.matches(i, r, mode)
		if canceled := ctx.Err(); canceled != nil {
			return nil, canceled
		}
		if err != nil {
			return nil, fmt.Errorf("validate %s %s: %w", r.Kind, r.Path, err)
		}
		if !matches {
			v := copyRecord(r)
			return &v, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}
func (s Set) matches(index int, r Record, mode Mode) (bool, error) {
	switch r.Kind {
	case Env:
		v, present := os.LookupEnv(r.Path)
		return present == r.Present && v == string(r.Value), nil
	case Content:
		before, err := os.Stat(r.Path)
		if err != nil {
			if missing(err) {
				return false, nil
			}
			return false, err
		}
		if before.IsDir() {
			return false, nil
		}
		s.data.mu.Lock()
		hint := s.data.hints[index]
		if hint != nil {
			copy := *hint
			hint = &copy
		}
		s.data.mu.Unlock()
		if mode == Quick && unchangedStamp(before, hint) {
			return true, nil
		}
		observed := now()
		contents, err := os.ReadFile(r.Path)
		if err != nil {
			if missing(err) {
				return false, nil
			}
			return false, err
		}
		sum := sha256.Sum256(contents)
		if hex.EncodeToString(sum[:]) != string(r.Value) {
			return false, nil
		}
		after, err := os.Stat(r.Path)
		if err == nil && sameInfo(before, after) && int64(len(contents)) == before.Size() && observed.UnixNano() >= 0 {
			stamp := makeStamp(before, observed)
			s.data.mu.Lock()
			s.data.hints[index] = stamp
			s.data.mu.Unlock()
		}
		return true, nil
	case State:
		switch r.Op {
		case CompilerFile, RegularFile, Directory, Stat:
			info, err := os.Stat(r.Path)
			if err != nil && !missing(err) {
				return false, err
			}
			var v Value
			switch r.Op {
			case CompilerFile:
				if err == nil && !info.IsDir() {
					v = True
				} else {
					v = False
				}
			case RegularFile:
				if err == nil && info.Mode().IsRegular() {
					v = True
				} else {
					v = False
				}
			case Directory:
				if err == nil && info.IsDir() {
					v = True
				} else {
					v = False
				}
			case Stat:
				if err != nil {
					v = Missing
				} else if info.IsDir() {
					v = DirectoryValue
				} else {
					v = File
				}
			}
			return v == r.Value, nil
		case ReadFile:
			_, err := os.ReadFile(r.Path)
			return failedReadMatches(err, r.Value)
		case ReadDirectory:
			_, err := os.ReadDir(r.Path)
			return failedReadMatches(err, r.Value)
		}
	case Listing:
		entries, err := os.ReadDir(r.Path)
		if err != nil {
			if missing(err) {
				return false, nil
			}
			return false, err
		}
		actual := make([][2]string, 0, len(entries))
		for _, entry := range entries {
			if _, ignored := slices.BinarySearch(r.Ignore, entry.Name()); ignored {
				continue
			}
			kind := entry.Type()
			if r.Op == Physical {
				k := Other
				switch {
				case kind&fs.ModeSymlink != 0:
					k = Symlink
				case kind.IsDir():
					k = DirectoryValue
				case kind.IsRegular():
					k = File
				}
				actual = append(actual, [2]string{entry.Name(), string(k)})
				continue
			}
			var link bool
			if kind&fs.ModeSymlink != 0 || kind&fs.ModeIrregular != 0 && isReparsePoint(filepath.Join(r.Path, entry.Name())) {
				link = true
				info, err := os.Stat(filepath.Join(r.Path, entry.Name()))
				if err != nil {
					continue
				}
				kind = info.Mode()
			}
			switch {
			case kind.IsDir():
				actual = append(actual, [2]string{entry.Name(), string(DirectoryValue)})
			case kind.IsRegular():
				actual = append(actual, [2]string{entry.Name(), string(File)})
			default:
				continue
			}
			if link {
				actual = append(actual, [2]string{entry.Name(), string(Symlink)})
			}
		}
		normalized, err := normalize(Record{Kind: Listing, Path: r.Path, Op: r.Op, Entries: actual, Ignore: r.Ignore})
		if err != nil {
			return false, err
		}
		return slices.Equal(normalized.Entries, r.Entries), nil
	case Link:
		value, err := os.Readlink(r.Path)
		if err != nil {
			if missing(err) || errors.Is(err, syscall.EINVAL) {
				return false, nil
			}
			return false, err
		}
		return value == string(r.Value), nil
	case Resolved:
		value, err := filepath.EvalSymlinks(r.Path)
		if err != nil {
			if missing(err) {
				return false, nil
			}
			return false, err
		}
		return filepath.Clean(value) == string(r.Value), nil
	}
	return false, fmt.Errorf("invalid observation")
}
func failedReadMatches(err error, expected Value) (bool, error) {
	if err == nil {
		return false, nil
	}
	if missing(err) {
		return expected == Missing, nil
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EISDIR) {
		return expected == Unreadable, nil
	}
	return false, err
}
