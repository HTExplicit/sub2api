package service

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/stretchr/testify/require"
)

// SHA-256 of the UTF-8 bytes of "sk-test-key", as the admin page computes it.
const accountSearchTestKeyDigest = "0d62f396c1317066f55a96086517047c737087c61eb2bf016b72e6298927b15b"

func accountSearchPredicateSQL(predicates ...dbpredicate.Account) (string, []any) {
	selector := entsql.Dialect(dialect.Postgres).Select().From(entsql.Table(dbaccount.Table))
	for _, predicate := range predicates {
		predicate(selector)
	}
	return selector.Query()
}

func TestAccountSearchPredicateKeyDigestTermMatchesAPIKeyDigestOnly(t *testing.T) {
	sum := sha256.Sum256([]byte("sk-test-key"))
	term := AccountSearchKeyDigestPrefix + hex.EncodeToString(sum[:])
	require.Equal(t, "sha256:"+accountSearchTestKeyDigest, term)

	const keyDigestSQL = `jsonb_typeof("accounts"."credentials" -> 'api_key') = 'string'` +
		` AND encode(sha256(convert_to(btrim("accounts"."credentials" ->> 'api_key', E' \t\n\r'), 'UTF8')), 'hex') = `

	query, args := accountSearchPredicateSQL(AccountSearchPredicate(term))
	require.Equal(t, `SELECT * FROM "accounts" WHERE `+keyDigestSQL+`$1`, query)
	require.Equal(t, []any{accountSearchTestKeyDigest}, args)

	// Next to another filter the condition stays one AND-ed unit and the digest
	// takes the next placeholder.
	query, args = accountSearchPredicateSQL(dbaccount.PlatformEQ(PlatformOpenAI), AccountSearchPredicate(term))
	require.Equal(t, `SELECT * FROM "accounts" WHERE "accounts"."platform" = $1 AND (`+keyDigestSQL+`$2)`, query)
	require.Equal(t, []any{PlatformOpenAI, accountSearchTestKeyDigest}, args)
}

func TestAccountSearchPredicateOtherTermsSearchNameOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		search string
	}{
		{"plain text", "Alpha"},
		{"plaintext key", "sk-test-key"},
		{"uppercase hex", AccountSearchKeyDigestPrefix + strings.ToUpper(accountSearchTestKeyDigest)},
		{"63 hex characters", AccountSearchKeyDigestPrefix + accountSearchTestKeyDigest[:63]},
		{"65 hex characters", AccountSearchKeyDigestPrefix + accountSearchTestKeyDigest + "0"},
		{"non-hex character", AccountSearchKeyDigestPrefix + accountSearchTestKeyDigest[:63] + "g"},
		{"uppercase prefix", "SHA256:" + accountSearchTestKeyDigest},
		{"leading text", "key " + AccountSearchKeyDigestPrefix + accountSearchTestKeyDigest},
		{"trailing text", AccountSearchKeyDigestPrefix + accountSearchTestKeyDigest + " key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query, args := accountSearchPredicateSQL(AccountSearchPredicate(tc.search))
			require.Equal(t, `SELECT * FROM "accounts" WHERE "accounts"."name" ILIKE $1`, query)

			wantQuery, wantArgs := accountSearchPredicateSQL(dbaccount.NameContainsFold(tc.search))
			require.Equal(t, wantQuery, query)
			require.Equal(t, wantArgs, args)
		})
	}
}
