//go:build !adtr_ci_dbadmission

package main

func configureCIAdmission(string) (<-chan struct{}, func(), error) { return nil, func() {}, nil }
