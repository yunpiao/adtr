package auth

import (
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http"
)

func directoryV4PathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return directoryPathAllowedForProfile(method, path, grants, directoryHTTPV4)
}
func (s *Service) DirectoryV4Handler(store *domains.Store, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveDirectoryForProfile(w, r, store, engine, directoryHTTPV4)
	})
}
func directoryV4CredentialUsePathAllowed(method, path, role string, grants map[string]AccessAuth) bool {
	return credentialUsePathAllowedForPurpose(method, path, role, grants, credentialuse.DirectoryV4Purpose)
}
func (s *Service) DirectoryV4CredentialUseHandler(store *credentialuse.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveCredentialUseForPurpose(w, r, store, credentialuse.DirectoryV4Purpose)
	})
}
