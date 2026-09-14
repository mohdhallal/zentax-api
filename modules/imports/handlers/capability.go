package handlers

import (
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// The capabilities the import routes declare. They are the SAME ones the
// single-record routes declare for the same records, and there is deliberately
// no "import" capability of its own: importing is not a separate power, it is
// the power to write these records exercised many times at once. A role that
// may not create an entity by hand cannot acquire the ability by uploading a
// file, and one that may create entities does not need a second grant to
// upload a list of them.
//
// This is the tenant-wide gate, enforced by RequireCapability before the
// handler runs. The per-entity half — which subtree a scoped grant may write
// in — is the Authorizer's, applied per row in the use case.
func writeCapability(kind domain.Kind) authz.Capability {
	if kind == domain.KindEntityObligations {
		return authz.EntityObligationWrite
	}
	return authz.EntityWrite
}

func readCapability(kind domain.Kind) authz.Capability {
	if kind == domain.KindEntityObligations {
		return authz.EntityObligationRead
	}
	return authz.EntityRead
}
