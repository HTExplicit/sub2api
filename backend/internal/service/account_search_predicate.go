package service

import (
	"crypto/sha256"
	"strings"

	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"

	entsql "entgo.io/ent/dialect/sql"
)

// AccountSearchKeyDigestPrefix marks an account search value as an API key
// digest term: the prefix followed by the lowercase hex SHA-256 of the key's
// UTF-8 bytes. The admin page sends this term instead of the key, so the
// plaintext key never reaches the server.
const AccountSearchKeyDigestPrefix = "sha256:"

// AccountSearchPredicate is the account search condition shared by every admin
// account query builder, so list, facets, export and bulk-by-filter select the
// same accounts. A key digest term matches the accounts whose
// credentials.api_key hashes to it and never matches names; any other value is
// a case-insensitive substring of the account name.
func AccountSearchPredicate(search string) dbpredicate.Account {
	digest, ok := accountSearchKeyDigest(search)
	if !ok {
		return dbaccount.NameContainsFold(search)
	}
	return dbpredicate.Account(func(s *entsql.Selector) {
		credentials := s.C(dbaccount.FieldCredentials)
		s.Where(entsql.And(
			// ->> renders any JSON value as text; only a JSON string is a key.
			entsql.P(func(b *entsql.Builder) {
				b.WriteString("jsonb_typeof(").
					Ident(credentials).
					WriteString(" -> 'api_key') = 'string'")
			}),
			// Stored keys are used with surrounding whitespace trimmed, so the
			// digest is taken over the trimmed value as well.
			entsql.P(func(b *entsql.Builder) {
				b.WriteString("encode(sha256(convert_to(btrim(").
					Ident(credentials).
					WriteString(` ->> 'api_key', E' \t\n\r'), 'UTF8')), 'hex') = `).
					Arg(digest)
			}),
		))
	})
}

// accountSearchKeyDigest returns the hex digest carried by a key digest term.
// Only the exact form is a digest term: the prefix followed by 64 lowercase
// hex characters and nothing else.
func accountSearchKeyDigest(search string) (string, bool) {
	digest, ok := strings.CutPrefix(search, AccountSearchKeyDigestPrefix)
	if !ok || len(digest) != 2*sha256.Size {
		return "", false
	}
	for i := 0; i < len(digest); i++ {
		if c := digest[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return digest, true
}
