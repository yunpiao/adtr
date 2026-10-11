package auth

import (
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http"
)

func directoryV3PathAllowed(method, path string, grants map[string]AccessAuth) bool {
	return directoryPathAllowedForProfile(method, path, grants, directoryHTTPV3)
}
func (s *Service) DirectoryV3Handler(store *domains.Store, engine *tasks.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveDirectoryForProfile(w, r, store, engine, directoryHTTPV3)
	})
}
func directoryV3CredentialUsePathAllowed(method, path, role string, grants map[string]AccessAuth) bool {
	return credentialUsePathAllowedForPurpose(method, path, role, grants, credentialuse.DirectoryV3Purpose)
}
func (s *Service) DirectoryV3CredentialUseHandler(store *credentialuse.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveCredentialUseForPurpose(w, r, store, credentialuse.DirectoryV3Purpose)
	})
}
