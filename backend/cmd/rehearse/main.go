// Command rehearse is the Go half of scripts/rehearse_cutover.sh, which proves
// the whole platform-split story end to end on disk against databases that
// exist only for the run (build-phases.txt 3i-c). It has three subcommands:
//
//	rehearse hash <password>   print a bcrypt hash (to seed a pre-split manager)
//	rehearse fakes             run local stand-ins for Africa's Talking (SMS) and
//	                           PayHero (STK push), recording what they were sent
//	rehearse smoke             drive the REAL platform-api and rentals-api over
//	                           HTTP and assert the cutover worked
//
// The smoke test is a script of numbered steps; every one prints PASS or FAIL
// and any FAIL makes the exit status non-zero. Nothing here is imported by the
// product binaries.
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

var _ = stdlib.GetDefaultDriver // register the pgx database/sql driver

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: rehearse <hash|fakes|smoke|payhero>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "hash":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: rehearse hash <password>")
			os.Exit(2)
		}
		h, err := bcrypt.GenerateFromPassword([]byte(os.Args[2]), bcrypt.DefaultCost)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Print(string(h))
	case "fakes":
		runFakes()
	case "smoke":
		os.Exit(runSmoke())
	case "payhero":
		os.Exit(runPayheroCLI(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------- fakes

type captured struct {
	mu    sync.Mutex
	items []map[string]any
}

func (c *captured) add(m map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, m)
}

func (c *captured) serve(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c.items)
}

func runFakes() {
	var sms, pushes captured

	smsMux := http.NewServeMux()
	smsMux.HandleFunc("/version1/messaging", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sms.add(map[string]any{"to": r.Form.Get("to"), "message": r.Form.Get("message")})
		fmt.Fprintf(os.Stderr, "SMS to %s: %s\n", r.Form.Get("to"), r.Form.Get("message"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"SMSMessageData":{"Message":"Sent to 1/1","Recipients":[{"statusCode":101,"number":%q,"status":"Success"}]}}`, r.Form.Get("to"))
	})
	smsMux.HandleFunc("/_sent", sms.serve)

	payMux := http.NewServeMux()
	pend := &pendingPushes{auto: os.Getenv("FAKE_PAYHERO_AUTOCALLBACK") == "true", delay: 3 * time.Second}
	payMux.HandleFunc("/api/v2/payments", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		pushes.add(body)
		checkout := fmt.Sprintf("ws_CO_fake_%d", time.Now().UnixNano())
		pend.register(body, checkout)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"success":true,"status":"QUEUED","reference":"fake-%d","CheckoutRequestID":%q}`, time.Now().UnixNano(), checkout)
	})
	payMux.HandleFunc("/_pushes", pushes.serve)
	payMux.HandleFunc("/_complete", pend.completeHandler)
	// Channel registry: an empty account, so a manager's "register" creates one.
	var channelMu sync.Mutex
	nextChannel := int64(7000)
	payMux.HandleFunc("/api/v2/payment_channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("[]"))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		channelMu.Lock()
		nextChannel++
		id := nextChannel
		channelMu.Unlock()
		body["id"], body["is_active"] = id, true
		_ = json.NewEncoder(w).Encode(body)
	})

	errc := make(chan error, 2)
	go func() { errc <- http.ListenAndServe(env("FAKE_SMS_ADDR", "127.0.0.1:14901"), smsMux) }()
	go func() { errc <- http.ListenAndServe(env("FAKE_PAYHERO_ADDR", "127.0.0.1:14902"), payMux) }()
	fmt.Fprintln(os.Stderr, "rehearse fakes: SMS and PayHero stand-ins listening")
	fmt.Fprintln(os.Stderr, "rehearse fakes:", <-errc)
	os.Exit(1)
}

// ---- fake PayHero: STK outcomes
//
// Real PayHero answers an STK push with "queued" and, once the tenant has
// acted on their phone, POSTs the outcome to the push's callback_url (which
// already carries the shared-secret token). The fake does the same: with
// FAKE_PAYHERO_AUTOCALLBACK=true (make dev/up sets it) every push is completed
// ~3s later, the outcome chosen by the LAST DIGIT of the amount:
//
//	0 (or anything else) success   1 cancelled by the user
//	2 insufficient funds           3 no callback at all (a timeout)
//
// Any push can also be completed by hand: GET /_complete?result=...&ref=...
// (ref defaults to the latest unanswered push), which is what
// `rehearse payhero result=... [ref=...]` / `make dev/payhero` call.
type pendingPush struct {
	ref, checkout, callback, phone string
	amount                         int64
	done                           bool
}

type pendingPushes struct {
	mu    sync.Mutex
	list  []*pendingPush
	auto  bool
	delay time.Duration
}

func (p *pendingPushes) register(body map[string]any, checkout string) {
	amount, _ := body["amount"].(float64)
	ref, _ := body["external_reference"].(string)
	cb, _ := body["callback_url"].(string)
	phone, _ := body["phone_number"].(string)
	push := &pendingPush{ref: ref, checkout: checkout, callback: cb, phone: phone, amount: int64(amount)}
	p.mu.Lock()
	p.list = append(p.list, push)
	p.mu.Unlock()
	if !p.auto {
		return
	}
	result := "success"
	switch push.amount % 10 {
	case 1:
		result = "cancelled"
	case 2:
		result = "insufficient"
	case 3:
		return // no callback: the pay page must time out on its own
	}
	go func() {
		time.Sleep(p.delay)
		p.complete(push, result)
	}()
}

// complete posts the outcome of one push to its callback URL and logs one line.
func (p *pendingPushes) complete(push *pendingPush, result string) string {
	p.mu.Lock()
	push.done = true
	p.mu.Unlock()
	resp := map[string]any{"Amount": push.amount, "CheckoutRequestID": push.checkout, "ExternalReference": push.ref, "Phone": push.phone}
	switch result {
	case "success":
		resp["MpesaReceiptNumber"] = fmt.Sprintf("FAKE%09d", time.Now().UnixNano()%1_000_000_000)
		resp["ResultCode"], resp["ResultDesc"], resp["Status"] = 0, "The service request is processed successfully.", "Success"
	case "cancelled":
		resp["ResultCode"], resp["ResultDesc"], resp["Status"] = 1032, "Request cancelled by user", "Failed"
	case "insufficient":
		resp["ResultCode"], resp["ResultDesc"], resp["Status"] = 1, "The balance is insufficient for the transaction", "Failed"
	default: // "failed"
		resp["ResultCode"], resp["ResultDesc"], resp["Status"] = 2001, "The initiator information is invalid", "Failed"
		result = "failed"
	}
	phone := push.phone
	if phone != "" && !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}
	if push.callback == "" {
		fmt.Fprintf(os.Stderr, "[payhero] STK %d to %s -> %s, no callback_url on the push\n", push.amount, phone, result)
		return "no callback_url"
	}
	raw, _ := json.Marshal(map[string]any{"response": resp})
	res, err := http.Post(push.callback, "application/json", bytes.NewReader(raw))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[payhero] STK %d to %s -> %s, callback failed: %v\n", push.amount, phone, result, err)
		return err.Error()
	}
	_ = res.Body.Close()
	fmt.Fprintf(os.Stderr, "[payhero] STK %d to %s -> %s, callback %d\n", push.amount, phone, result, res.StatusCode)
	return fmt.Sprintf("callback %d", res.StatusCode)
}

func (p *pendingPushes) completeHandler(w http.ResponseWriter, r *http.Request) {
	result, ref := r.URL.Query().Get("result"), r.URL.Query().Get("ref")
	if result == "" {
		result = "success"
	}
	p.mu.Lock()
	var target *pendingPush
	for _, c := range slices.Backward(p.list) {
		if (ref == "" && !c.done) || (ref != "" && c.ref == ref) {
			target = c
			break
		}
	}
	p.mu.Unlock()
	if target == nil {
		http.Error(w, "no matching push (give ref=<external reference>, or push first)", http.StatusNotFound)
		return
	}
	_, _ = fmt.Fprintf(w, "%s: %s\n", target.ref, p.complete(target, result))
}

// runPayheroCLI is `rehearse payhero result=<success|cancelled|insufficient|failed> [id=<reference>]`.
func runPayheroCLI(args []string) int {
	q := url.Values{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			fmt.Fprintln(os.Stderr, "usage: rehearse payhero result=<success|cancelled|insufficient|failed> [id=<external reference>]")
			return 2
		}
		if k == "id" {
			k = "ref"
		}
		q.Set(k, v)
	}
	res, err := http.Get("http://" + env("FAKE_PAYHERO_ADDR", "127.0.0.1:14902") + "/_complete?" + q.Encode())
	if err != nil {
		fmt.Fprintln(os.Stderr, "is `make dev/up` running?", err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	out, _ := io.ReadAll(res.Body)
	fmt.Print(string(out))
	if res.StatusCode != 200 {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- smoke

type smoke struct {
	platform, rentals, sms, payhero string
	webhookSecret                   string
	platformLog                     string
	rentalsAdmin, platformAdmin     *sql.DB
	failed                          int
	n                               int
}

// client is one signed-in browser: a cookie jar shared across both APIs, as the
// real session cookie is (same host, ADR-0007), so a login on platform-api is
// what rentals-api then sees.
type client struct{ http *http.Client }

func newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{http: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
}

type reply struct {
	Status int
	Body   map[string]json.RawMessage
	Raw    string
}

func (c *client) do(method, target string, body any, headers map[string]string) reply {
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			buf, _ := json.Marshal(b)
			rd = bytes.NewReader(buf)
		}
	}
	req, err := http.NewRequest(method, target, rd)
	if err != nil {
		return reply{Status: -1, Raw: err.Error()}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return reply{Status: -1, Raw: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	r := reply{Status: resp.StatusCode, Raw: string(raw)}
	_ = json.Unmarshal(raw, &r.Body)
	return r
}

func (r reply) str(key string) string {
	var s string
	_ = json.Unmarshal(r.Body[key], &s)
	return s
}

// nested reads a dotted path of object keys, e.g. "manager.id".
func (r reply) nested(path string) string {
	cur := r.Body
	parts := strings.Split(path, ".")
	for i, p := range parts {
		raw, ok := cur[p]
		if !ok {
			return ""
		}
		if i == len(parts)-1 {
			var s any
			_ = json.Unmarshal(raw, &s)
			if s == nil {
				return ""
			}
			return fmt.Sprint(s)
		}
		var next map[string]json.RawMessage
		if json.Unmarshal(raw, &next) != nil {
			return ""
		}
		cur = next
	}
	return ""
}

func (s *smoke) step(name string, ok bool, detail string) {
	s.n++
	if ok {
		fmt.Printf("  PASS %2d  %s\n", s.n, name)
		return
	}
	s.failed++
	fmt.Printf("  FAIL %2d  %s\n         %s\n", s.n, name, strings.ReplaceAll(strings.TrimSpace(detail), "\n", "\n         "))
}

func (s *smoke) expect(name string, r reply, want int) bool {
	ok := r.Status == want
	s.step(name, ok, fmt.Sprintf("got HTTP %d, want %d: %s", r.Status, want, r.Raw))
	return ok
}

func (s *smoke) fatal(msg string) {
	fmt.Printf("  ABORT   %s\n", msg)
	s.failed++
}

var tokenRE = regexp.MustCompile(`\b[A-Z2-7]{26}\b`)

// emailedToken waits for the activation/invite email to `to` to appear in
// platform-api's log (no SMTP server: LogMailer logs the message) and returns
// the 26-character token in it.
func (s *smoke) emailedToken(to string) string {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(s.platformLog)
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "email (not sent") {
				continue
			}
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) != nil || rec["to"] != to {
				continue
			}
			if body, _ := rec["body"].(string); body != "" {
				if m := tokenRE.FindString(body); m != "" {
					return m
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return ""
}

func (s *smoke) fakeList(base, path string) []map[string]any {
	resp, err := http.Get(base + path)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var out []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func runSmoke() int {
	s := &smoke{
		platform:      env("PLATFORM_URL", "http://127.0.0.1:14100"),
		rentals:       env("RENTALS_URL", "http://127.0.0.1:14000"),
		sms:           env("FAKE_SMS_URL", "http://127.0.0.1:14901"),
		payhero:       env("FAKE_PAYHERO_URL", "http://127.0.0.1:14902"),
		webhookSecret: env("WEBHOOK_SECRET", "rehearsal-webhook-secret"),
		platformLog:   env("PLATFORM_LOG", "platform-api.log"),
	}
	var err error
	if s.rentalsAdmin, err = sql.Open("pgx", os.Getenv("RENTALS_ADMIN_DSN")); err != nil {
		fmt.Println("RENTALS_ADMIN_DSN:", err)
		return 1
	}
	if s.platformAdmin, err = sql.Open("pgx", os.Getenv("PLATFORM_ADMIN_DSN")); err != nil {
		fmt.Println("PLATFORM_ADMIN_DSN:", err)
		return 1
	}
	defer func() { _ = s.rentalsAdmin.Close(); _ = s.platformAdmin.Close() }()

	s.run()
	fmt.Printf("\nsmoke: %d steps, %d failed\n", s.n, s.failed)
	if s.failed > 0 {
		return 1
	}
	return 0
}

func (s *smoke) run() {
	p := func(path string) string { return s.platform + "/v1" + path }
	r := func(path string) string { return s.rentals + "/v1" + path }
	const password = "Rehearsal-Pass-1"
	stamp := fmt.Sprint(time.Now().UnixNano() % 1e9)
	email := "smoke" + stamp + "@rehearsal.example.com"
	staffEmail := "clerk" + stamp + "@rehearsal.example.com"

	fmt.Println("\n== both APIs are up")
	hp, hr := (&client{http: http.DefaultClient}).do("GET", p("/healthcheck"), nil, nil), (&client{http: http.DefaultClient}).do("GET", r("/healthcheck"), nil, nil)
	if !s.expect("platform-api healthcheck", hp, 200) || !s.expect("rentals-api healthcheck", hr, 200) {
		s.fatal("an API is not answering; nothing else can run")
		return
	}

	fmt.Println("\n== the backfilled pre-split customer keeps working after the cutover")
	old := newClient()
	oldEmail, oldPass := env("OLD_FIRM_EMAIL", "oldfirm@rehearsal.example.com"), env("OLD_FIRM_PASSWORD", "Old-Password-1")
	if s.expect("old firm signs in on platform-api with its pre-split password", old.do("POST", p("/sessions"), map[string]string{"email": oldEmail, "password": oldPass}, nil), 200) {
		res := old.do("GET", r("/properties"), nil, nil)
		s.step("...and rentals-api (shared session) serves its pre-split property", res.Status == 200 && strings.Contains(res.Raw, "Old Court"), res.Raw)
		sess := old.do("GET", p("/sessions?product=rentals"), nil, nil)
		s.step("...with its backfilled subscription: access is subscribed", sess.nested("access.state") == "subscribed", sess.Raw)
	}

	fmt.Println("\n== signup, activate, sign in (platform-api)")
	owner := newClient()
	reg := owner.do("POST", p("/managers"), map[string]string{"firm_name": "Smoke Firm " + stamp, "username": "smoke" + stamp, "email": email, "phone": "+254722000999", "password": password}, nil)
	if !s.expect("signup", reg, 202) {
		return
	}
	managerID := reg.nested("manager.id")
	s.expect("a pending firm can sign in on platform-api", owner.do("POST", p("/sessions"), map[string]string{"email": email, "password": password}, nil), 200)
	s.expect("...but rentals-api keeps the product closed until activation (403)", owner.do("GET", r("/properties"), nil, nil), 403)
	tok := s.emailedToken(email)
	s.step("the activation email carried a token", tok != "", "no email for "+email+" in "+s.platformLog)
	if tok == "" {
		return
	}
	s.expect("activate", owner.do("PUT", p("/managers/activated"), map[string]string{"token": tok}, nil), 200)
	s.expect("sign in", owner.do("POST", p("/sessions"), map[string]string{"email": email, "password": password}, nil), 200)
	sess := owner.do("GET", p("/sessions?product=rentals"), nil, nil)
	s.step("session reports a running Rentals trial", sess.nested("access.state") == "trial", sess.Raw)

	fmt.Println("\n== rentals-api accepts the platform session")
	s.expect("anonymous request is refused", newClient().do("GET", r("/properties"), nil, nil), 401)
	s.expect("manager reaches the dashboard", owner.do("GET", r("/properties"), nil, nil), 200)
	s.expect("the removed local login is gone", owner.do("POST", r("/sessions"), map[string]string{"email": email, "password": password}, nil), 404)

	fmt.Println("\n== create a property and a unit, run the rent")
	ll := owner.do("POST", r("/landlords"), map[string]any{"name": "Wanjiku Estates", "phone": "+254733100200"}, nil)
	s.expect("create landlord", ll, 201)
	landlordID := ll.nested("landlord.id")
	slug := "smoke-" + stamp
	prop := owner.do("POST", r("/properties"), map[string]any{"landlord_id": landlordID, "name": "Smoke Court", "location": "Nairobi", "slug": slug,
		"water_rate_per_unit": "150"}, nil)
	s.expect("create property", prop, 201)
	propertyID := prop.nested("property.id")
	// Registered through the API (not typed in as a raw channel id), so the
	// channel is claimed in platform's payment_channels first: the one-owner
	// guard the collections inbox depends on.
	chn := owner.do("POST", r("/properties/"+propertyID+"/payment-channel"), map[string]any{"type": "paybill", "short_code": 123456, "account_number": "SMOKE" + stamp, "description": "Smoke Court collections"}, nil)
	s.expect("register the property's PayHero channel (claimed in platform)", chn, 200)
	channelID := chn.nested("channel.id")
	unit := owner.do("POST", r("/properties/"+propertyID+"/units"), map[string]any{"unit_code": "A1"}, nil)
	s.expect("create unit", unit, 201)
	unitID := unit.nested("unit.id")
	s.expect("create lease", owner.do("POST", r("/units/"+unitID+"/leases"), map[string]any{"tenant_name": "JOHN KAMAU", "primary_phone": "+254722000001",
		"rent_amount": "5000", "start_date": time.Now().AddDate(0, -1, 0).Format("2006-01-02")}, nil), 201)
	s.expect("set the rent schedule", owner.do("PUT", r("/properties/"+propertyID+"/rent-schedule"), []map[string]any{{"unit_id": unitID, "rent_amount": "5000"}}, nil), 200)
	period := time.Now().Format("2006-01") + "-01"
	run := owner.do("POST", r("/properties/"+propertyID+"/rent/generate?period="+period), nil, nil)
	s.step("rent run bills the unit", run.Status == 201 && run.nested("run.billed") == "1", run.Raw)
	again := owner.do("POST", r("/properties/"+propertyID+"/rent/generate?period="+period), nil, nil)
	s.step("re-running the period bills nothing twice", again.nested("run.billed") == "0", again.Raw)

	fmt.Println("\n== usage reaches the platform")
	s.eventually("module_usage for the firm shows 1 unit (rentals-api reported it to platform)", func() bool {
		var n int
		return s.platformAdmin.QueryRow(`SELECT billable_count FROM module_usage WHERE manager_id = $1 AND product = 'rentals'`, managerID).Scan(&n) == nil && n == 1
	})

	fmt.Println("\n== the tenant pays from the public pay page (OTP, STK push, webhook)")
	tenant := newClient()
	base := r("/pay/" + slug + "/A1")
	s.expect("pay page loads without a login", tenant.do("GET", base, nil, nil), 200)
	s.expect("balances are closed before the phone is proved", tenant.do("GET", base+"/balances", nil, nil), 401)
	s.expect("request an OTP", tenant.do("POST", base+"/otp", map[string]string{"phone": "0722000001"}, nil), 202)
	var code string
	s.eventually("the OTP text reached the (fake) SMS gateway", func() bool {
		for _, m := range s.fakeList(s.sms, "/_sent") {
			if msg, _ := m["message"].(string); strings.Contains(msg, "payment code") {
				code = regexp.MustCompile(`\b\d{6}\b`).FindString(msg)
			}
		}
		return code != ""
	})
	ver := tenant.do("POST", base+"/otp/verify", map[string]string{"phone": "0722000001", "code": code}, nil)
	s.expect("verify the OTP", ver, 200)
	auth := map[string]string{"Authorization": "Bearer " + ver.str("token")}
	bal := tenant.do("GET", base+"/balances", nil, auth)
	s.step("balances show the 5,000 rent owed", bal.Status == 200 && strings.Contains(bal.Raw, "5000.00"), bal.Raw)
	intent := tenant.do("POST", base+"/intent", map[string]any{"lines": []map[string]string{{"type": "rent", "amount": "5000"}}}, auth)
	s.expect("start the payment (STK push to the fake PayHero)", intent, 202)
	var ref string
	if pushes := s.fakeList(s.payhero, "/_pushes"); len(pushes) > 0 {
		ref, _ = pushes[len(pushes)-1]["external_reference"].(string)
	}
	s.step("PayHero was asked to prompt the tenant's phone", ref != "", "no push captured")
	receipt := "REHEARSE" + stamp
	callback := `{"response":{"Amount":5000,"CheckoutRequestID":"ws_CO_1","ExternalReference":"` + ref + `","MpesaReceiptNumber":"` + receipt + `","Phone":"254722000001","ResultCode":0,"Status":"Success"}}`
	hook := r("/webhooks/payhero/collections")
	s.expect("an unauthenticated callback is refused", tenant.do("POST", hook, callback, nil), 401)
	s.expect("the callback with the shared secret is accepted", tenant.do("POST", hook+"?token="+s.webhookSecret+"&channel_id="+channelID, callback, nil), 200)
	s.eventually("the payment posted once", func() bool { return s.paymentRows(receipt) == 1 })
	s.expect("PayHero replays the callback", tenant.do("POST", hook+"?token="+s.webhookSecret+"&channel_id="+channelID, callback, nil), 200)
	time.Sleep(1500 * time.Millisecond)
	s.step("...and it still posted exactly once", s.paymentRows(receipt) == 1, fmt.Sprintf("payments rows for %s = %d", receipt, s.paymentRows(receipt)))
	s.step("the channel is owned by this property in platform's payment_channels", s.channelOwner(channelID) == managerID, "owner: "+s.channelOwner(channelID))
	s.eventually("the payment inbox in platform claimed the receipt once", func() bool {
		var n int
		return s.platformAdmin.QueryRow(`SELECT count(*) FROM payment_inbox WHERE mpesa_receipt = $1`, receipt).Scan(&n) == nil && n == 1
	})
	bal = tenant.do("GET", base+"/balances", nil, auth)
	s.step("the tenant's rent balance is cleared", bal.Status == 200 && !strings.Contains(bal.Raw, `"5000.00"`), bal.Raw)

	// The dev path a human walks: the fake PayHero itself answers the push
	// (same code as its 3-second auto-callback, here triggered through
	// /_complete so the run is deterministic) and the pay page reaches
	// "completed" with a receipt, the unit's ledger and the property's
	// collected figure move, and platform's inbox ends 'posted'.
	fmt.Println("\n== the fake PayHero completes a payment end to end")
	collected := func() float64 {
		var v float64
		_, _ = fmt.Sscan(owner.do("GET", r("/properties/"+propertyID), nil, nil).nested("property.summary.rent_collected"), &v)
		return v
	}
	ledgerBefore := owner.do("GET", r("/units/"+unitID+"/ledger/rent"), nil, nil)
	collectedBefore := collected()
	intent2 := tenant.do("POST", base+"/intent", map[string]any{"lines": []map[string]string{{"type": "rent", "amount": "1000"}}}, auth)
	s.expect("start a second payment (1,000)", intent2, 202)
	intentID := intent2.nested("intent.id")
	pending := tenant.do("GET", r("/pay/intents/")+intentID+"/status", nil, auth)
	s.step("the pay page sees it pending", pending.Status == 200 && pending.nested("intent.status") == "pending", pending.Raw)
	done, err := http.Get(s.payhero + "/_complete?result=success")
	if err == nil {
		_ = done.Body.Close()
	}
	s.step("the fake PayHero posted the success callback", err == nil && done.StatusCode == 200, fmt.Sprint(err))
	var fin reply
	s.eventually("the pay page reaches completed with a receipt", func() bool {
		fin = tenant.do("GET", r("/pay/intents/")+intentID+"/status", nil, auth)
		return fin.Status == 200 && fin.nested("intent.status") == "completed" && fin.nested("intent.receipt.mpesa_receipt") != ""
	})
	fakeReceipt := fin.nested("intent.receipt.mpesa_receipt")
	s.step("the receipt is the fake's (FAKE...) for 1,000", strings.HasPrefix(fakeReceipt, "FAKE") && strings.Contains(fin.Raw, "1000.00"), fin.Raw)
	ledgerAfter := owner.do("GET", r("/units/"+unitID+"/ledger/rent"), nil, nil)
	s.step("the payment is on the unit's rent ledger", s.paymentRows(fakeReceipt) == 1 && ledgerAfter.Status == 200 && ledgerAfter.Raw != ledgerBefore.Raw, "before: "+ledgerBefore.Raw+"\nafter: "+ledgerAfter.Raw)
	s.step("the property's collected figure rose by 1,000", collected()-collectedBefore == 1000, fmt.Sprintf("before %.2f after %.2f", collectedBefore, collected()))
	s.eventually("payment_inbox marked the receipt 'posted'", func() bool {
		var st string
		return s.platformAdmin.QueryRow(`SELECT status FROM payment_inbox WHERE mpesa_receipt = $1`, fakeReceipt).Scan(&st) == nil && st == "posted"
	})

	intent3 := tenant.do("POST", base+"/intent", map[string]any{"lines": []map[string]string{{"type": "rent", "amount": "1000"}}}, auth)
	s.expect("start a third payment, to be cancelled", intent3, 202)
	cancelled, err := http.Get(s.payhero + "/_complete?result=cancelled")
	if err == nil {
		_ = cancelled.Body.Close()
	}
	s.step("the fake PayHero posted the cancelled callback", err == nil && cancelled.StatusCode == 200, fmt.Sprint(err))
	s.eventually("the pay page sees failed with the reason", func() bool {
		st := tenant.do("GET", r("/pay/intents/")+intent3.nested("intent.id")+"/status", nil, auth)
		return st.nested("intent.status") == "failed" && strings.Contains(st.Raw, "cancelled")
	})

	// 3i-d: a channel id typed in by hand (no PayHero registration call) is
	// claimed in platform at save time, so a webhook for it goes through the
	// payment inbox rather than the local-only fallback (which writes no
	// inbox row at all).
	fmt.Println("\n== a hand-typed channel id is claimed in platform and its webhook uses the inbox")
	typed1, typed2 := "HAND1-"+stamp, "HAND2-"+stamp
	handProp := owner.do("POST", r("/properties"), map[string]any{"landlord_id": landlordID, "name": "Hand Typed Plaza", "location": "Nairobi", "slug": "hand-" + stamp,
		"payhero_channel_id": typed1}, nil)
	s.expect("create a property with a hand-typed channel id", handProp, 201)
	handID := handProp.nested("property.id")
	s.step("the channel is claimed in platform for that property", s.channelClaim(typed1) == "rentals/"+handID, "claim: "+s.channelClaim(typed1))
	s.expect("the same channel id on a second property is refused", owner.do("POST", r("/properties"), map[string]any{"landlord_id": landlordID, "name": "Duplicate Plaza", "location": "Nairobi", "slug": "dup-" + stamp,
		"payhero_channel_id": typed1}, nil), 409)
	s.expect("a channel already owned by the first property is refused when patched on it", owner.do("PATCH", r("/properties/"+propertyID), map[string]any{"payhero_channel_id": typed1}, nil), 409)
	s.expect("changing the channel id on the property", owner.do("PATCH", r("/properties/"+handID), map[string]any{"payhero_channel_id": typed2}, nil), 200)
	s.step("the new channel is claimed and the old claim released", s.channelClaim(typed2) == "rentals/"+handID && s.channelClaim(typed1) == "", "new: "+s.channelClaim(typed2)+" old: "+s.channelClaim(typed1))
	organic := "HANDTYPED" + stamp
	organicBody := `{"response":{"Amount":1200,"MpesaReceiptNumber":"` + organic + `","Phone":"254700000009","ResultCode":0,"Status":"Success"}}`
	s.expect("PayHero reports a paybill payment on the hand-typed channel", tenant.do("POST", hook+"?token="+s.webhookSecret+"&channel_id="+typed2, organicBody, nil), 200)
	s.eventually("the receipt was claimed in platform's payment inbox (not the local fallback)", func() bool {
		var n int
		return s.platformAdmin.QueryRow(`SELECT count(*) FROM payment_inbox WHERE mpesa_receipt = $1 AND channel_id = $2`, organic, typed2).Scan(&n) == nil && n == 1
	})
	s.eventually("...and posted locally under that property", func() bool { return s.paymentRows(organic) == 1 })
	s.expect("clearing the channel id", owner.do("PATCH", r("/properties/"+handID), map[string]any{"payhero_channel_id": ""}, nil), 200)
	s.step("a channel with recorded payments keeps its claim on release", s.channelClaim(typed2) == "rentals/"+handID, "claim: "+s.channelClaim(typed2))

	fmt.Println("\n== staff: invited and signed in on platform-api, role-gated on rentals-api")
	inv := owner.do("POST", p("/staff"), map[string]any{"name": "Clerk", "email": staffEmail, "role": "officer", "module_access": []string{"rentals"}}, nil)
	s.expect("owner invites a staff member", inv, 201)
	stTok := s.emailedToken(staffEmail)
	s.step("the invite email carried a token", stTok != "", "no email for "+staffEmail)
	staff := newClient()
	s.expect("staff activates", staff.do("PUT", p("/staff/activated"), map[string]string{"token": stTok, "password": password}, nil), 200)
	s.expect("staff signs in on platform-api", staff.do("POST", p("/sessions"), map[string]string{"email": staffEmail, "password": password}, nil), 200)
	s.expect("staff (officer) reads the portfolio on rentals-api", staff.do("GET", r("/properties"), nil, nil), 200)
	s.expect("...but is refused a write above their role (accountant needed)", staff.do("POST", r("/landlords"), map[string]any{"name": "X", "phone": "+254711111111"}, nil), 403)

	fmt.Println("\n== Super Admin: oversight on platform-api")
	admin := newClient()
	s.expect("admin signs in on platform-api", admin.do("POST", p("/admin/sessions"), map[string]string{"email": env("ADMIN_EMAIL", ""), "password": env("ADMIN_PASSWORD", "")}, nil), 200)
	list := admin.do("GET", p("/admin/managers?page_size=100"), nil, nil)
	s.step("manager list includes the new firm, with its 1 unit and 1 staff", list.Status == 200 && strings.Contains(list.Raw, managerID), list.Raw)
	one := admin.do("GET", p("/admin/managers/"+managerID), nil, nil)
	s.step("manager detail reports units from module_usage and the staff count", one.nested("manager.unit_count") == "1" && one.nested("manager.staff_count") == "1", one.Raw)
	s.expect("a manager cannot reach admin oversight", owner.do("GET", p("/admin/managers"), nil, nil), 403)
	sysHealthRep := admin.do("GET", p("/admin/system/health"), nil, nil)
	s.step("platform-api's system health reports both services (platform database and rentals-api's own report)", sysHealthRep.Status == 200 && sysHealthRep.nested("status") == "available" &&
		sysHealthRep.nested("services.platform.status") == "ok" && sysHealthRep.nested("services.rentals.status") == "ok" && sysHealthRep.nested("services.rentals.report.database.status") == "ok", sysHealthRep.Raw)
	s.expect("a manager cannot read the system health", owner.do("GET", p("/admin/system/health"), nil, nil), 403)
	s.expect("the admin session also works on rentals-api's own admin routes", admin.do("GET", r("/admin/backups"), nil, nil), 200)
	s.expect("...but is not a manager there", admin.do("GET", r("/properties"), nil, nil), 403)
	subs := admin.do("GET", p("/admin/subscriptions?product=rentals&status=active"), nil, nil)
	s.step("subscriptions list filters by product and status (shows the backfilled firm)", subs.Status == 200 && strings.Contains(subs.Raw, "Old Plan"), subs.Raw)

	fmt.Println("\n== suspend: both APIs refuse the manager and the staff; the public pay page keeps working")
	susp := admin.do("POST", p("/admin/managers/"+managerID+"/suspend"), nil, nil)
	s.step("suspend revokes the manager's and the staff's sessions", susp.Status == 200 && susp.nested("sessions_revoked") == "2", susp.Raw)
	s.expect("manager -> platform-api is refused (401)", owner.do("GET", p("/account"), nil, nil), 401)
	s.expect("manager -> rentals-api is refused (401)", owner.do("GET", r("/properties"), nil, nil), 401)
	s.expect("staff -> platform-api is refused (401)", staff.do("GET", p("/account"), nil, nil), 401)
	s.expect("staff -> rentals-api is refused (401)", staff.do("GET", r("/properties"), nil, nil), 401)
	s.expect("the suspended manager cannot sign in again", newClient().do("POST", p("/sessions"), map[string]string{"email": email, "password": password}, nil), 403)
	s.expect("an unrelated firm is untouched", old.do("GET", r("/properties"), nil, nil), 200)
	s.expect("tenants can still reach the pay page", newClient().do("GET", base, nil, nil), 200)
	s.step("the suspension is on the audit trail as the admin", s.auditCount(managerID, "manager.suspended") == 1, fmt.Sprint(s.auditCount(managerID, "manager.suspended")))

	fmt.Println("\n== reinstate")
	s.expect("reinstate", admin.do("POST", p("/admin/managers/"+managerID+"/reinstate"), nil, nil), 200)
	again2 := newClient()
	s.expect("manager signs in again", again2.do("POST", p("/sessions"), map[string]string{"email": email, "password": password}, nil), 200)
	res := again2.do("GET", r("/properties"), nil, nil)
	s.step("...and finds their data intact on rentals-api", res.Status == 200 && strings.Contains(res.Raw, "Smoke Court"), res.Raw)
}

func (s *smoke) paymentRows(receipt string) int {
	var n int
	_ = s.rentalsAdmin.QueryRow(`SELECT count(*) FROM payments WHERE mpesa_receipt = $1`, receipt).Scan(&n)
	return n
}

func (s *smoke) channelOwner(channelID string) string {
	var owner string
	_ = s.platformAdmin.QueryRow(`SELECT manager_id::text FROM payment_channels WHERE channel_id = $1`, channelID).Scan(&owner)
	return owner
}

// channelClaim is "product/owner_id" for channelID in platform's
// payment_channels, or "" if nobody holds it.
func (s *smoke) channelClaim(channelID string) string {
	var product string
	var owner sql.NullString
	if err := s.platformAdmin.QueryRow(`SELECT product, owner_id::text FROM payment_channels WHERE channel_id = $1`, channelID).Scan(&product, &owner); err != nil {
		return ""
	}
	return product + "/" + owner.String
}

func (s *smoke) auditCount(managerID, action string) int {
	var n int
	_ = s.platformAdmin.QueryRow(`SELECT count(*) FROM audit_log WHERE manager_id = $1 AND action = $2 AND actor_type = 'admin'`, managerID, action).Scan(&n)
	return n
}

// eventually polls cond (work done in a background goroutine on the server)
// for up to 10 seconds.
func (s *smoke) eventually(name string, cond func() bool) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			s.step(name, true, "")
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.step(name, false, "condition not met within 10s")
}
