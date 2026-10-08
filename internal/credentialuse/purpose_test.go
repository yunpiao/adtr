package credentialuse

import (
	"context"
	"errors"
	"testing"

	"github.com/yunpiao/adtr/internal/tasks"
)

func requirePurposeError(t *testing.T, err error) {
	t.Helper()
	var failure *Error
	if !errors.As(err, &failure) || failure.Status != 422 || failure.Code != "unsupported_credential_purpose" {
		t.Fatalf("expected unsupported purpose, got %v", err)
	}
}

func TestPurposeValidationPreservesLegacyBoundary(t *testing.T) {
	for _, purpose := range []string{Purpose, DirectoryPurpose, DirectoryV2Purpose} {
		in := validInput()
		in.Purpose = purpose
		if err := ValidateInputForPurpose("/grant", &in, purpose); err != nil {
			t.Fatal(err)
		}
		in.ExpectedGrantRevision = "1"
		if err := ValidateInputForPurpose("/revoke", &in, purpose); err != nil {
			t.Fatal(err)
		}
		for _, other := range []string{Purpose, DirectoryPurpose, DirectoryV2Purpose} {
			if other != purpose {
				requirePurposeError(t, ValidateInputForPurpose("/grant", &in, other))
				for _, path := range []string{"/grant", "/revoke"} {
					_, err := New().ReplayForPurposeTx(context.Background(), nil, tasks.Principal{}, path, in, nil, other)
					requirePurposeError(t, err)
					_, err = New().MutateForPurposeTx(context.Background(), nil, tasks.Principal{}, path, in, nil, other)
					requirePurposeError(t, err)
				}
			}
		}
	}
	for _, purpose := range []string{DirectoryPurpose, DirectoryV2Purpose} {
		in := validInput()
		in.Purpose = purpose
		requirePurposeError(t, ValidateInput("/grant", &in))
		// Legacy entry points reject either directory input before using SQL.
		_, err := New().ReplayTx(context.Background(), nil, tasks.Principal{}, "/grant", in, nil)
		requirePurposeError(t, err)
		_, err = New().MutateTx(context.Background(), nil, tasks.Principal{}, "/grant", in, nil)
		requirePurposeError(t, err)
	}
	if !DirectoryConsumerEnabled || !ConsumerEnabled || !consumerEnabledForPurpose(DirectoryPurpose) || consumerEnabledForPurpose("unknown") {
		t.Fatal("compiled purpose capability registry is inconsistent")
	}
}

func TestUnknownPurposesNeverReachSQL(t *testing.T) {
	ctx := context.Background()
	s := New()
	p := tasks.Principal{TenantID: "tenant", ActorID: 1}
	for _, purpose := range []string{"", "domain.directory_read ", "DOMAIN.DIRECTORY_READ", "domain.directory_read.v1", "domain.directory_read.v02", "domain.directory_read.v2 ", "DOMAIN.DIRECTORY_READ.V2", "domain.directory_read.v2\x00", "domain.directory_read.v3", "domain.install", "domain.connection_test\x00"} {
		t.Run(purpose, func(t *testing.T) {
			in := validInput()
			in.Purpose = purpose
			requirePurposeError(t, ValidateInputForPurpose("/grant", &in, purpose))
			_, err := s.AccountsForPurposeTx(ctx, nil, "tenant", nil, Filter{}, purpose)
			requirePurposeError(t, err)
			_, err = s.GrantsForPurposeTx(ctx, nil, "tenant", "account", nil, purpose)
			requirePurposeError(t, err)
			_, err = s.RolesForPurposeTx(ctx, nil, "tenant", "account", nil, purpose)
			requirePurposeError(t, err)
			_, err = s.EffectiveForPurposeTx(ctx, nil, p, "platform_admin", "account", nil, purpose)
			requirePurposeError(t, err)
			_, err = s.ReceiptForPurposeTx(ctx, nil, p, "key", nil, purpose)
			requirePurposeError(t, err)
			_, err = s.ReplayForPurposeTx(ctx, nil, p, "/grant", in, nil, purpose)
			requirePurposeError(t, err)
			_, err = s.MutateForPurposeTx(ctx, nil, p, "/grant", in, nil, purpose)
			requirePurposeError(t, err)
		})
	}
}

func TestDirectoryMutationStillRequiresExactRevisionsAndIntent(t *testing.T) {
	for _, purpose := range []string{DirectoryPurpose, DirectoryV2Purpose} {
		t.Run(purpose, func(t *testing.T) { testDirectoryMutationRevisions(t, purpose) })
	}
	p := tasks.Principal{TenantID: "tenant", ActorID: 1}
	seen := map[string]bool{}
	for _, purpose := range []string{Purpose, DirectoryPurpose, DirectoryV2Purpose} {
		in := validInput()
		in.Purpose = purpose
		digest := fingerprint(p, "/grant", in)
		if seen[digest] {
			t.Fatal("idempotency digest does not distinguish exact purposes")
		}
		seen[digest] = true
	}
}

func testDirectoryMutationRevisions(t *testing.T, purpose string) {
	in := validInput()
	in.Purpose = purpose
	for _, mutate := range []func(*Input){
		func(i *Input) { i.ExpectedAccountRevision = "01" },
		func(i *Input) { i.ExpectedCredentialRevision = "0" },
		func(i *Input) { i.ExpectedGrantRevision = "-1" },
		func(i *Input) { i.RoleID = "admin" },
		func(i *Input) { i.IdempotencyKey = "" },
	} {
		bad := in
		mutate(&bad)
		if ValidateInputForPurpose("/grant", &bad, purpose) == nil {
			t.Fatal("invalid directory mutation accepted")
		}
	}
	if ValidateInputForPurpose("/revoke", &in, purpose) == nil || ValidateInputForPurpose("/grant", nil, purpose) == nil {
		t.Fatal("missing grant or input accepted")
	}
}

func TestDirectoryV2KnownPurposeDoesNotEnableAnUnregisteredConsumer(t *testing.T) {
	if DirectoryV2ConsumerEnabled || consumerEnabledForPurpose(DirectoryV2Purpose) {
		t.Fatal("unregistered dictionary 2 consumer was advertised as enabled")
	}
	for _, purpose := range []string{DirectoryPurpose, DirectoryV2Purpose} {
		if !isDirectoryPurpose(purpose) || validatePurpose(purpose) != nil {
			t.Fatal("exact directory purpose not recognized")
		}
	}
	for _, purpose := range []string{Purpose, "", "domain.directory_read.v3", "domain.directory_read.v2 "} {
		if isDirectoryPurpose(purpose) {
			t.Fatal("non-directory or unknown purpose selected directory eligibility")
		}
	}
}
