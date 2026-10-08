package auth

import (
	"net/http"

	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
)

// Only server-owned entry points select this profile; request data never does.
type directoryHTTPProfile uint8

const (
	directoryHTTPV1 directoryHTTPProfile = iota + 1
	directoryHTTPV2
)

func directoryHTTPEndpoint(profile directoryHTTPProfile) (prefix, kind string) {
	switch profile {
	case directoryHTTPV1:
		return "/api/directory", domains.DirectoryKindName
	case directoryHTTPV2:
		return "/api/directory/v2", domains.DirectoryV2KindName
	default:
		return "", ""
	}
}

func isDirectoryTaskKind(kind string) bool {
	return kind == domains.DirectoryKindName || kind == domains.DirectoryV2KindName
}

func directoryV2PathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return directoryPathAllowedForProfile(method, path, grants, directoryHTTPV2)
}

func (s *Service) DirectoryV2Handler(store *domains.Store, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveDirectoryForProfile(w, r, store, engine, directoryHTTPV2)
	})
}
