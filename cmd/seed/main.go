// Command seed fills a running erp/credit with the seed data for people (be-assembly-standard's seed and test data
// conventions):
//
//   - access, by the authorization admin: the role maintain (the seed user) and read in a legal entity (CN01:
//     test.sales.cn and test.sales.multi; ES01: test.sales.es);
//   - policies: CN01 and US01 block a customer without a limit (no_limit), ES01 holds and counts them (unlimited);
//   - limits for every active customer role in CN01, ES01 and US01, in the role's currency, by the partner's place in
//     name order: 1,000,000; 200,000; 20,000 (a large order exceeds it); no amount (not under credit control); no limit
//     row (refused in CN01 and US01, counted in ES01);
//   - the first 200,000 limit in CN01 suspended (refuses new holds).
//
// A limit is looked up by partner, legal entity and currency before it is made, so a second run creates nothing. The
// legal entities and partners come from mdm/org and mdm/partner (their seeds run first: make seed).
//
//	SEED_IAM_URL (http://localhost:18200)    the identity member's development exchange
//	SEED_AUTHZ_URL (http://localhost:18223)  the authorization member's effect point
//	SEED_ADMIN (test.alice)                  the authorization admin (the install claim)
//	SEED_USER (test.bob), SEED_PASSWORD      the user that writes the data; the test accounts' password
//	ERP_CREDIT_URL (http://localhost:18088), MDM_ORG_URL (http://localhost:18083), MDM_PARTNER_URL (http://localhost:18225)
package main

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"time"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/seed"
)

const prefix = "「本地测试」"

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "seed erp/credit: "+format+"\n", a...)
	os.Exit(1)
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}

type client struct{ seed.Client }

func (c client) must(method, path string, body any, header ...string) map[string]any {
	out, err := c.Must(method, path, body, header...)
	check(err)
	return out
}

// segment grants an operation in the given legal entities (all when none).
func segment(resource, op string, legalEntities ...string) sdkauthz.Segment {
	s := seed.Segment(resource, op)
	le := sdkauthz.Atom{Kind: "all"}
	if len(legalEntities) > 0 {
		le = sdkauthz.Atom{Kind: "in", Values: legalEntities}
	}
	s.Atoms = map[string]sdkauthz.Atom{"legal_entity": le}
	return s
}

func reads(legalEntities ...string) []sdkauthz.Segment {
	return []sdkauthz.Segment{segment("erp.credit.limit", "erp.credit.limit.read", legalEntities...),
		segment("erp.credit.policy", "erp.credit.policy.read", legalEntities...)}
}

type role struct{ partner, name, le, currency string }

// customerRoles are the active customer roles of every partner, in the partners' name order.
func customerRoles(c client, les map[string]string) []role {
	var partners []map[string]any
	cursor := ""
	for {
		page := c.must("GET", "/api/partner/partners?limit=200&cursor="+url.QueryEscape(cursor), nil)
		for _, p := range page["partners"].([]any) {
			partners = append(partners, p.(map[string]any))
		}
		if cursor, _ = page["next_cursor"].(string); cursor == "" {
			break
		}
	}
	sort.Slice(partners, func(i, j int) bool { return partners[i]["name"].(string) < partners[j]["name"].(string) })
	code := map[string]string{}
	for k, v := range les {
		code[v] = k
	}
	var out []role
	for _, p := range partners {
		if p["state"] != "active" {
			continue
		}
		for _, r := range c.must("GET", "/api/partner/partners/"+p["id"].(string)+"/roles", nil)["roles"].([]any) {
			r := r.(map[string]any)
			le, _ := r["legal_entity_id"].(string)
			cur, _ := r["default_currency"].(string)
			if r["role"] == "customer" && r["state"] == "active" && cur != "" && (code[le] == "CN01" || code[le] == "ES01" || code[le] == "US01") {
				out = append(out, role{p["id"].(string), p["name"].(string), le, cur})
			}
		}
	}
	return out
}

func main() {
	base := env("ERP_CREDIT_URL", "http://localhost:18088")
	if !seed.Ready(base) {
		fail("erp/credit at %s is not ready", base)
	}
	pw := os.Getenv("SEED_PASSWORD")
	if pw == "" {
		fail("SEED_PASSWORD is not set (the test accounts' password in Casdoor)")
	}
	iam, user, admin := env("SEED_IAM_URL", "http://localhost:18200"), env("SEED_USER", "test.bob"), env("SEED_ADMIN", "test.alice")
	accounts, err := seed.Accounts(iam, pw, admin, user, "test.sales.cn", "test.sales.es", "test.sales.multi")
	check(err)
	t := accounts[user].Token
	les := map[string]string{}
	org := client{seed.NewClient(env("MDM_ORG_URL", "http://localhost:18083"), t)}
	for _, e := range org.must("GET", "/api/org/legal-entities?limit=200", nil)["legal_entities"].([]any) {
		e := e.(map[string]any)
		les[e["code"].(string)] = e["id"].(string)
	}
	for _, code := range []string{"CN01", "ES01", "US01"} {
		if les[code] == "" {
			fail("mdm/org has no legal entity %s: run its seed first", code)
		}
	}
	a, err := seed.NewAuthz(env("SEED_AUTHZ_URL", "http://localhost:18223"), accounts[admin].Token, "erp/credit")
	check(err)
	_, err = a.GrantRoles([]seed.RoleSpec{
		{Key: "maintain", Name: prefix + "信用维护", To: []string{user}, Segments: append(reads(),
			segment("erp.credit.limit", "erp.credit.limit.manage"), segment("erp.credit.limit", "erp.credit.limit.request"),
			segment("erp.credit.policy", "erp.credit.policy.manage"))},
		{Key: "read-cn01", Name: prefix + "信用只读（CN01）", To: []string{"test.sales.cn", "test.sales.multi"}, Segments: reads(les["CN01"])},
		{Key: "read-es01", Name: prefix + "信用只读（ES01）", To: []string{"test.sales.es"}, Segments: reads(les["ES01"])},
	}, accounts)
	check(err)

	c := client{seed.NewClient(base, t)}
	for code, policy := range map[string]string{"CN01": "block", "ES01": "unlimited", "US01": "block"} {
		st, out, err := c.UntilGranted(30*time.Second, "PUT", "/api/credit/policies/"+les[code], map[string]any{"without_limit": policy})
		check(err)
		if st != 200 {
			fail("policy of %s: %d %v", code, st, out)
		}
	}
	roles := customerRoles(client{seed.NewClient(env("MDM_PARTNER_URL", "http://localhost:18225"), t)}, les)
	made, suspended := 0, false
	for i, r := range roles {
		var limit any
		switch i % 5 {
		case 0:
			limit = "1000000"
		case 1:
			limit = "200000"
		case 2:
			limit = "20000"
		case 3:
			limit = nil // not under credit control
		case 4:
			continue // no limit row
		}
		page := c.must("GET", "/api/credit/limits?partner_id="+r.partner+"&legal_entity_id="+r.le+"&currency="+r.currency, nil)
		var id, state string
		if l := page["limits"].([]any); len(l) > 0 {
			id, state = l[0].(map[string]any)["id"].(string), l[0].(map[string]any)["state"].(string)
		} else {
			out := c.must("POST", "/api/credit/limits", map[string]any{"partner_id": r.partner, "legal_entity_id": r.le,
				"currency": r.currency, "limit": limit}, "Idempotency-Key", "seed:erp/credit:limit:"+r.partner+":"+r.le+":"+r.currency)
			id, state = out["id"].(string), out["state"].(string)
			made++
		}
		if i%5 == 1 && r.le == les["CN01"] && !suspended {
			suspended = true
			if state == "active" {
				c.must("PATCH", "/api/credit/limits/"+id, map[string]any{"state": "suspended"})
			}
		}
	}
	fmt.Printf("seed erp/credit: %d customer roles, %d limits made\n", len(roles), made)
}
