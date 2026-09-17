package admin

// Plain server-rendered HTML — no client JS framework, no template
// hierarchy beyond this one layout. The panel has exactly the screens
// listed in spec 23.4-23.6; do not add a generic table-browser template
// here (spec 23.7).

const layoutTmpl = `{{define "page"}}<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>{{.Title}} — Willcoll Admin</title>
  <style>
    body { font-family: system-ui, sans-serif; margin: 0; background: #F3F4F6; color: #1F2320; }
    nav { background: #1F2320; padding: 12px 24px; display: flex; gap: 16px; align-items: center; }
    nav a { color: #FFFFFF; text-decoration: none; font-size: 14px; }
    nav a.brand { font-weight: 700; margin-right: 16px; }
    main { padding: 24px; max-width: 960px; margin: 0 auto; }
    table { width: 100%; border-collapse: collapse; background: #FFFFFF; }
    th, td { text-align: left; padding: 8px 12px; border-bottom: 1px solid #E2E5E9; font-size: 14px; }
    form.inline { display: inline; }
    select, input[type=text], input[type=password] { padding: 4px 8px; }
    button { padding: 6px 12px; cursor: pointer; }
    .error { color: #C6362E; }
    .login-box { max-width: 320px; margin: 80px auto; background: #FFFFFF; padding: 24px; border-radius: 8px; }
    .login-box label { display: block; margin-bottom: 12px; }
  </style>
</head>
<body>
  {{if .ShowNav}}<nav>
    <a class="brand" href="/admin/">Willcoll Admin</a>
    <a href="/admin/organizations">Organizations</a>
    <a href="/admin/addons">Score Add-on</a>
    <a href="/admin/audit-log">Audit Log</a>
    <a href="/admin/operations">Operations</a>
    <form class="inline" method="post" action="/admin/logout" style="margin-left:auto"><button>Log out</button></form>
  </nav>{{end}}
  <main>{{template "body" .Data}}</main>
</body>
</html>{{end}}`

const loginTmpl = `{{define "body"}}
<div class="login-box">
  <h1>Admin Login</h1>
  {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
  <form method="post" action="/admin/login">
    <label>Username <input type="text" name="username" required></label>
    <label>Password <input type="password" name="password" required></label>
    <button type="submit">Log in</button>
  </form>
</div>
{{end}}`

const dashboardTmpl = `{{define "body"}}
<h1>Platform Admin</h1>
<ul>
  <li><a href="/admin/organizations">Organizations</a> — subscription tier, billing status, unit counts</li>
  <li><a href="/admin/addons">Verified Property Score Subscribers</a></li>
  <li><a href="/admin/audit-log">Cross-org Audit Log</a></li>
  <li><a href="/admin/operations">Operations</a> — ledger drift, stuck payments</li>
</ul>
{{end}}`

const organizationsTmpl = `{{define "body"}}
<h1>Organizations</h1>
<table>
  <tr><th>Name</th><th>Units</th><th>Created</th><th>Tier</th><th>Billing status</th><th></th></tr>
  {{range .Orgs}}
  <tr>
    <td>{{.Name}}</td>
    <td>{{.UnitCount}}</td>
    <td>{{.CreatedAt.Format "2006-01-02"}}</td>
    <td colspan="3">
      <form class="inline" method="post" action="/admin/organizations/{{.ID}}">
        <select name="subscription_tier">
          <option value="starter" {{if eq .SubscriptionTier "starter"}}selected{{end}}>starter</option>
          <option value="growth" {{if eq .SubscriptionTier "growth"}}selected{{end}}>growth</option>
          <option value="professional" {{if eq .SubscriptionTier "professional"}}selected{{end}}>professional</option>
          <option value="enterprise" {{if eq .SubscriptionTier "enterprise"}}selected{{end}}>enterprise</option>
        </select>
        <select name="billing_status">
          <option value="active" {{if eq .BillingStatus "active"}}selected{{end}}>active</option>
          <option value="past_due" {{if eq .BillingStatus "past_due"}}selected{{end}}>past_due</option>
          <option value="suspended" {{if eq .BillingStatus "suspended"}}selected{{end}}>suspended</option>
        </select>
        <button type="submit">Save</button>
      </form>
    </td>
  </tr>
  {{end}}
</table>
{{end}}`

const addonsTmpl = `{{define "body"}}
<h1>Verified Property Score Subscribers</h1>
<table>
  <tr><th>Organization</th><th>Monthly fee (KES)</th><th>Activated</th></tr>
  {{range .Subs}}
  <tr><td>{{.OrganizationName}}</td><td>{{.MonthlyFeeKES}}</td><td>{{.ActivatedAt.Format "2006-01-02"}}</td></tr>
  {{end}}
</table>
{{end}}`

const auditLogTmpl = `{{define "body"}}
<h1>Audit Log (cross-org, last 200)</h1>
<table>
  <tr><th>When</th><th>Org</th><th>Action</th><th>Entity</th><th>Source</th></tr>
  {{range .Entries}}
  <tr><td>{{.CreatedAt.Format "2006-01-02 15:04"}}</td><td>{{.OrganizationID}}</td><td>{{.Action}}</td><td>{{.EntityType}}</td><td>{{.Source}}</td></tr>
  {{end}}
</table>
{{end}}`

const operationsTmpl = `{{define "body"}}
<h1>Operations</h1>
<h2>Ledger drift</h2>
<table>
  <tr><th>Account</th><th>Org</th><th>Balance</th><th>Entry sum</th></tr>
  {{range .Drift}}
  <tr><td>{{.AccountID}}</td><td>{{.OrganizationID}}</td><td>{{.Balance}}</td><td>{{.EntrySum}}</td></tr>
  {{else}}
  <tr><td colspan="4">No drift detected.</td></tr>
  {{end}}
</table>
<h2>Stuck gateway payments (PENDING &gt; 1h)</h2>
<table>
  <tr><th>Reference</th><th>Org</th><th>Channel</th><th>Amount</th><th>Received</th></tr>
  {{range .Stuck}}
  <tr><td>{{.GatewayRef}}</td><td>{{.OrganizationID}}</td><td>{{.Channel}}</td><td>{{.Amount}}</td><td>{{.ReceivedAt.Format "2006-01-02 15:04"}}</td></tr>
  {{else}}
  <tr><td colspan="5">None stuck.</td></tr>
  {{end}}
</table>
{{end}}`
