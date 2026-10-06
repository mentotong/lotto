# Lotaria — 4D / 3D / 2D ticket seller

A small Go + [templ](https://templ.guide) + [htmx](https://htmx.org) + [Tailwind CSS](https://tailwindcss.com) app for selling lottery tickets.

## Quick start

```sh
sudo pacman -S go          # Omarchy/Arch; other systems: https://go.dev/dl/
cd lotto
go run .                   # first run downloads two small Go libraries
```

1. Open http://localhost:8080/setup on this computer and create the admin account.
2. **Agents** → add your sales points (e.g. `AV`).
3. **Users** → add sellers and pick their agent.
4. Admins enter each draw's results on the main page (**Enter results**); selling is at `/sell`, where people land after signing in.
5. To let sellers use it on their phones, run `./share.sh` instead of `go run .` (see [Share it on the internet](#share-it-on-the-internet-ngrok)).

## Features

- **Results** (`/`, the main page): the latest draw's 1st, 2nd and 3rd prize, plus 10 Starter and 10 Consolation numbers, laid out like the usual results board. Arrows go to earlier/later draws and the date opens a date picker (`/?date=2026-10-04`). The page is **public**, so buyers can check results without an account; signed-out visitors see a small header with **Staff sign in**. Only **published** results appear there.

### Activity log (admins only)

**Activity** (`/activity`) records who did what and when, with the person's IP address:
- **Sign-in:** sign-ins and sign-outs, failed sign-ins, lockouts, and password changes.
- **Sales:** every ticket sold, with its serial, total, draw and buyer.
- **Changes:** users, agents, price lists, results (draft, publish, correct, unpublish) and the app setup.
  - Each edit says what changed, e.g. `role seller → admin; status active → disabled`.
- **Security:** a signed-in user opening an admin-only page.

Using the log:
- **Highlighting:** failed sign-ins, lockouts and refused pages are shown in red.
- **Search and filters:** search by name, username, serial or IP address, and filter by area and date.
  - Click a person's name to see only their actions. **Activity** on a user's page does the same.
- **Download CSV:** saves the current results, for example for an auditor.

The log is kept in its own file next to the data file, `lotto-activity.jsonl`, with one line per entry. Entries are only ever added. Each entry carries a SHA-256 hash of itself plus the entry before it, so editing or deleting a line breaks the chain. The page checks the chain on start-up and shows a red warning naming the first entry that doesn't match.

The chain catches casual edits. Someone with full access to the server could still rewrite the whole file, so download the CSV regularly and keep copies somewhere else.

### App setup (admins only)

**Setup** (`/setup/app`) changes how the app presents itself. Changes apply to everyone straight away:

- **Name and logo.** The app name appears in the header, page titles, receipts, the results page, the sign-in page and the shared images. The optional tagline goes under the name on receipts. The logo (PNG, JPEG or GIF, up to 1 MB) replaces the ticket icon everywhere and is used as the browser icon. It is stored next to the data file as `lotto-logo` and served at `/brand/logo`. SVG isn't accepted, because SVG files can carry scripts.
- **Colours.** The **main colour** covers the header, buttons and headings. The **accent colour** ("red pen") covers the handwritten numbers, totals and the Sell button. Both carry white text, so a colour that is too light (contrast below 4.5:1) is refused.
- **Money.** The currency symbol (e.g. `$`, `US$`) and whether it goes before or after the amount. Amounts already sold keep their value.
- **Receipts and results.**
  - A receipt note, phone and address are printed at the bottom of every receipt and ticket image.
  - A results note (e.g. draw times) goes under the results, together with the phone and address.
- **Selling and sign-in.**
  - How many lines a new sell form starts with (default 3).
  - The most lines one ticket can have (default 50, up to 100).
  - How many hours a sign-in lasts (default 12).

A live preview on the page shows the receipt header with the new name, logo and colours before you save. Sellers can't open the page; it returns 403.

### Sharing results

Under every published draw there's **Share results** (for everyone, signed in or not). It draws the results board as an image (1080×1610, with a QR code and link back to that draw) in the browser:

- **Send image** opens the phone's share menu: WhatsApp, Facebook, Messenger, Telegram, Instagram, SMS… with the picture and a short message ("Lotaria results for Sun 04 Oct 26: 1st 2812, 2nd 3006, 3rd 0429. All results: …").
- **WhatsApp** / **Facebook** buttons share the message and link (useful on computers, where browsers can't hand images to other apps).
- **Save image** downloads the PNG to post anywhere; **Copy link** copies the link to that draw.
- Drafts can't be shared. Run through `./share.sh` (ideally with `NGROK_URL`) so the link and QR work for people outside your network.

### Publishing results (admins only)

- **Enter results** (a dialog): the draw date and 23 four-digit numbers, all different, no future dates. The cursor jumps to the next box after 4 digits; pasting a list of numbers (e.g. copied from a message) fills all the boxes in reading order.
- **Save draft** keeps the results private: only admins see them, in a dashed frame with a "Draft, not published" banner. Buyers and sellers asking for that date see "No results for this date", and the main page keeps showing the latest published draw.
- **Publish** (from the form, or from the draft banner with a confirmation) makes them public on `/` for everyone, signed in or not.
- **Correct** changes published results; everyone sees the change straight away and it's recorded.
- **Unpublish** takes published results back to a draft (for results that went out wrong).
- Each draw has a **History** for admins: saved as draft, published, corrected, unpublished — who and when. Results entered before this version count as published.
- Sellers and signed-out visitors can only view published results; the server refuses any attempt by them to enter, publish or unpublish.
- **Sell** (`/sell`): numbers are typed in PIN-style boxes (one digit per box, numbers only; the cursor jumps to the next box, Backspace goes back, Enter moves to the next line). Like the paper slip, **a number always ends in the last box**: 4D fills all 4 boxes, 3D leaves the 1st box empty (✕623), 2D leaves the first two empty (✕✕23) — tap the box where the number starts. Pasting `623` fills ✕623. **A number can appear only once per ticket**; a repeated line turns red and isn't counted. The form starts with **3 lines**; **+ Add line** (htmx) adds more up to **50**, typing past the last line adds one automatically, and ✕ removes a line. The server re-checks everything (digits only, ends in the last box, no gaps, no repeats, max 50 lines). Quantity multiplies the price. Game tags, line amounts and the total are calculated by the server as you type.
- **Ticket** (`/tickets/{id}`): printable slip with serial, draw date, agent and a table of Number, Game, Qty, Price and Amount (3D/2D bets show ✕ in the unused boxes, like the paper slip), plus a **QR code** for the buyer. After a sale the page opens with **Share with buyer**, **Print** and **Sell another**.
- **Share with buyer**: draws the receipt and its QR code into a PNG image in the browser. On phones, **Send image** opens the share sheet (WhatsApp, Messenger, SMS…); **Save image** downloads it; **Copy link** copies the ticket's check link.
- **Buyer's check page** (`/r/{code}`): what the QR code opens. No sign-in; it shows only that one ticket and confirms it's registered. Each ticket gets a random 12-character code, so links can't be guessed from serial numbers. Tickets from older versions get a code automatically.
- **Sales** (`/tickets`): tickets newest first with the total sold and totals per game. Search by serial, number, buyer, seller or agent; filter by draw date and (admins) agent. Totals always cover **all** matching tickets, not just the page on screen. Admins see the top 8 agents by sales; a user's page has **View sales**, which narrows the list to that seller. Sellers see only their own sales.

### Search and pages

Sales, Agents and Users all have a search box and are split into pages (25 tickets, 24 agents or 25 users per page), so they stay fast with thousands of records — tested with 150 agents, 600 users and 3,000 tickets, each page loading in under 35 ms.

- Results update as you type (after a short pause); the search box keeps the cursor.
- Search ignores upper/lower case and accents (`agencia` finds *Agência*), and every word you type must match.
- **Users:** search name, username or agent; filter by role, agent (or "No agent") and status.
- **Agents:** search code, name, phone or address; filter by status; sort by code, name or highest sales.
### Adding users and agents

**Add a user**, **Add an agent** and an agent's **Add seller** open a dialog over the page (a sheet from the bottom on phones) instead of a form at the bottom of the page. Mistakes are shown inside the dialog without losing what was typed. After saving, the dialog closes, a short confirmation appears and the list refreshes; a new agent opens its own page so its sellers can be added next. **Add seller**, or **Add a user** while the list is filtered to one agent, pre-selects that agent. Esc, ✕, Cancel or a tap outside closes it (a tap outside is ignored once something has been typed). The forms also work as normal pages at `/users/new` and `/agents/new`. The username "new" is reserved.

- An agent's page lists its first 10 users, with **See all** linking to the Users list filtered to that agent.
- The search and page number are kept in the address, so the back button, refresh and shared links show the same results.
- **Prices** (`/settings`): prices are kept as numbered **price lists** with a full history. See below.

### Price lists and history

- Selling always uses the **active** price list. Only one list can be active at a time.
- **New prices** (a dialog) adds a new list with an optional note. With **Activate now** on, it replaces the active list straight away; with it off, the list is saved as **Not activated** for later.
- Any older list can be **activated** again; the one in use is then **deactivated**. Each list shows its status (**Active**, **Deactivated** or **Not activated**) and a timeline of every activation and deactivation, with who did it and when, plus how many tickets were sold at its prices.
- **Deactivate (stop selling)** turns off the active list without a replacement: every agent's Sell page then shows "Selling is stopped" and the server refuses new tickets until a list is activated. Use it to close sales before a draw.
- Activating and deactivating ask for confirmation in a dialog. Lists are never deleted.
- Every ticket records the price list it was sold under; each line keeps its own price, so changes never alter sold tickets.
- Upgrading: the prices from earlier versions become price list #1, already active.

## Look and layout

The design follows the paper slip: everything the system prints (navigation, labels, ruled lines) is in **form blue**, and everything a person writes (the lottery numbers, totals, agent codes) is in **red pen**, in a handwritten face. A sold ticket is drawn as the slip itself.

- **Phones:** bottom tab bar (sellers: Results, Sell, Sales, Account; admins: Results, Sell, Sales, Agents, Users, Prices), number lines stack into two rows, and a checkout bar with the total and **Sell ticket** stays pinned above the tabs.
- **Signed out** (results, sign-in, the buyer's QR check page): no app navigation, just a small Lotaria header. On Results it has **Staff sign in**; on the sign-in page and a buyer's ticket page it has **Results** (the sign-in page also has **Back to results** under the form). The Lotaria name always goes to the results.
- **Desktop:** top navigation; on the sell page the date, buyer and total sit in a sidebar that stays in view while you scroll long tickets.
- Fonts: Archivo (interface) and Kalam (handwritten numbers), bundled in `static/fonts/` under the SIL Open Font License, so the app still works offline.
- Keyboard focus is always visible, and motion is turned off for people who ask their system for reduced motion.

## Agents, users and roles

**Agents** are sales points (a kiosk, shop or street seller) with a short code like `AV` that is printed on every ticket, plus a name, phone and address. Admins and managers manage them at `/agents`. Agent codes can't be changed once created (they're on sold tickets); agents can be disabled but not deleted, so sales history stays intact.

**Users** sign in with one of four roles. Owners and sellers belong to one agent; managers work for all agents; an admin's agent is optional.

| | Admin | Manager | Owner | Seller |
|---|---|---|---|---|
| Sell tickets | ✓ | – | ✓ | ✓ |
| Sales list | everyone's | everyone's | their agent's | own tickets |
| Users (`/users`) | everyone | everyone except admins | their agent's people; **adds and changes its sellers, with approval** | – |
| Roles they can give | all four | manager, owner, seller | seller | – |
| Approve new users and changes (`/approvals`) | ✓ | ✓ (not admins) | – (sees "Requests") | – |
| Reset other people's passwords | ✓ | ✓ | their agent's sellers | – |
| Agents (`/agents`) | ✓ | ✓ | – | – |
| Prices (`/settings`) | ✓ | ✓ | – | – |
| Activity log (`/activity`, read only) | ✓ | ✓ | their agent's people | – |
| Enter and publish results | ✓ | – | – | – |
| App setup (`/setup/app`) | ✓ | – | – | – |
| Change own password (`/account`) | ✓ | ✓ | ✓ | ✓ |

Nobody can delete or edit the activity log from the app, not even admins. Pages a role can't use answer "not allowed" (403), and the attempt is logged.

### Agent documents and verification

Adding an agent asks for the business name, the **owner's full name** and the agent's legal documents. Each document has a number, an optional expiry date, and a photo or scan (JPEG, PNG or PDF, up to 5 MB):

| Document | |
|---|---|
| Business registration certificate (SERVE), with registration number | required |
| Tax ID (TIN) certificate, with the TIN | required |
| Owner's ID document (Passport, Bilhete de Identidade or Kartaun Eleitoral) | required |
| Lottery or gaming licence | optional |
| Proof of premises (lease, ownership papers, or a letter from the Chefe de Suco) | optional |

Checks:
- Expired documents are refused.
- A registration or TIN number already used by another agent is refused.

**Verification:**
- A new agent waits for verification. Until it's verified, its owners and sellers can't sign in, and the sign-in page tells them why.
- Agents waiting for verification appear under **Approvals** for admins and managers, with a count on the tab.
- A manager can't verify an agent they added or whose documents they last changed. Admins can verify any agent.
- Verifying shows every document to open and check. Rejecting needs a reason. Uploading corrected documents puts a rejected agent back in the queue.
- If documents of a verified agent are replaced, it's flagged **Documents to review** but keeps working.

**Status badges:** the Agents page shows each agent's status:
- Verified
- Waiting for verification
- Rejected
- Documents to review
- Documents missing
- Document expired

The page can be filtered to "Waiting for verification" or "Not verified". Search also finds agents by owner or document number.

**Older agents:** agents added before this version keep working and show **Documents missing** until their documents are uploaded.

**Storage and logging:** documents are kept in the same private `lotto-documents/` folder as ID documents, and only admins and managers can open them. Every view, upload, verification and rejection is in the activity log.

### ID documents

Every new user needs a national ID document:
- **Type:** Passport, Bilhete de Identidade or Kartaun Eleitoral. Picking one shows the remaining fields.
- **Document number.**
- **Expiry date:** optional. An expired document is refused.
- **Photo or scan:** JPEG, PNG or PDF, up to 5 MB. On a phone the file picker can take the photo directly.

Checks:
- The file type is checked from the file's content, not its name.
- One document number can't be registered to two users, so a person can't hold two accounts.

Users added before this version can stay without a document. Once anyone starts filling one in, it has to be complete.

Storage and access:
- **Storage:** documents are kept in `lotto-documents/` next to the data file, under random names. The folder is readable only by the account running the app, and files are never placed in the web folders.
- **Who can open them:**
  - admins
  - managers, for non-admins
  - owners, for their agent's people
  - the person themself
- **Logging:** every time someone else opens a document, it is recorded in the activity log.
- **Clean-up:** replaced or rejected documents are deleted.

### Approvals

Sellers don't manage users. **Owners** add sellers to their agent and can change them (name, active, ID document), but none of it takes effect straight away.

How a request works:
1. It waits for approval. A new account can't sign in yet; signing in says it is waiting for approval.
2. **Managers and admins** see it under **Approvals**, with a count on the tab. Each request shows the ID document to check, plus **Approve** and **Reject**. Rejecting needs a reason.
3. The owner follows their requests under **Requests**, which shows waiting, approved, or rejected with the reason.
4. Approving a change applies it. Rejecting a new user leaves the account unable to sign in.

Rules:
- Users added or changed by managers and admins don't need approval.
- Nobody approves their own request.

Every request, approval and rejection is in the activity log.

- Every ticket stores the agent code/name and seller name **at sale time**, so moving a user to another agent later doesn't change old reports.
- Disabling an agent signs out all its sellers and blocks them from signing in until it's enabled again.
- An agent's page lists its users and has a "+ Add seller" shortcut that pre-selects the agent.

- **First run:** with no users yet, every page redirects to `/setup`, where you create the first admin. Then create agents, then their sellers.
- Passwords are hashed with PBKDF2-SHA256 (600,000 iterations, Go standard library). Minimum 8 characters.
- Sessions: random token in an HttpOnly, SameSite=Lax cookie, valid 12 hours. Sessions live in memory, so **restarting the server signs everyone out**.
- Disabling a user or resetting their password signs them out everywhere immediately.
- You can't disable yourself or remove your own admin role, and the app always keeps at least one active admin.
- 5 failed logins for the same username from the same IP lock that combination for 15 minutes.
- Cross-site form posts are rejected (Origin / Sec-Fetch-Site check) on top of SameSite cookies.
- **Upgrading:** existing admins and sellers keep their roles; add managers and owners as needed. The old "agent" role is renamed to "seller" automatically. Those sellers have no agent yet and **can't sign in until an admin assigns one** (they're marked "needs agent" on the Users page). Older tickets show no agent.

Forgot the only admin password? Stop the app, remove the `"users"` entry from `lotto.json` (or set it to `[]`), start again and go through `/setup`.

## How the single-page behaviour works

- `<body hx-boost="true" hx-target="#main">` turns every link and form into an htmx request that swaps only `#main`. The address bar, page title and back button keep working.
- The server checks the `HX-Request` header: htmx requests get only the content (plus the nav, swapped out-of-band to move the active tab); normal loads and refreshes get the full page. See `page()` in `main.go`.
- Lines: **+ Add line** posts the form to `/tickets/line`, which returns the new line (appended with `hx-swap="beforeend"`) plus the button and the "3 of 50 lines" counter out-of-band. Each line posts a key `k`, four `d` boxes and `qty`; the server matches them by position, so removing a line leaves no gaps, and the live-preview fragments are addressed by key (`game-<k>`), so they stay on the right line.
- Live amounts: the sell table posts the form to `/tickets/preview` on each keystroke (`hx-trigger="input delay:150ms"`), which returns out-of-band fragments for each line and the total.
- Hand-written JavaScript is limited to three small files: `static/pin.js` (PIN boxes' cursor movement, removing a line), `static/share.js` (drawing and sharing the receipt image) and `static/modal.js` (the add dialogs).
- Validation errors (422), "not allowed" (403) and login lockout (429) are shown in place; `htmx-config` in the layout tells htmx to swap those. When a session expires mid-use, the server answers htmx with `HX-Redirect: /login`.

htmx and the Tailwind browser build live in `static/` and are embedded into the binary, so the app works offline and needs no Node.js.

## Run

```sh
go run .
```

Open http://localhost:8080.

| Flag | Default | What it does |
|---|---|---|
| `-addr` | `:8080` | Port to listen on, e.g. `-addr :9000` |
| `-data` | `lotto.json` | Data file (tickets, users, agents, prices) |
| `-public-url` | the request's address | Address used in QR-code links, e.g. `https://my-lotto.ngrok-free.app` |
| `-trust-proxy` | off | Trust ngrok's / a proxy's forwarded headers (`share.sh` sets it) |

**Back up `lotto.json`** — it holds everything. Copy it while the app is stopped.

### Editing the app

The `*_templ.go` files are generated from the `.templ` files. After editing any `.templ` file, regenerate:

```sh
go install github.com/a-h/templ/cmd/templ@v0.3.960   # once
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.bashrc && source ~/.bashrc   # once
templ generate && go run .
```

Files in `static/` are built into the program, so after changing them, stop and start the app again.

## Share it on the internet (ngrok)

`share.sh` starts the app and opens an [ngrok](https://ngrok.com) tunnel, giving you a public `https://` address that sellers can open on their phones.

One-time setup:

```sh
yay -S ngrok                                 # Omarchy/Arch (other systems: https://ngrok.com/download)
# free account: https://dashboard.ngrok.com/signup, then copy your token from
# https://dashboard.ngrok.com/get-started/your-authtoken
ngrok config add-authtoken <your-token>
```

Every time:

```sh
./share.sh
```

Share the `https://…ngrok-free.app` address shown next to **Forwarding**. Press `Ctrl+C` to stop both the tunnel and the app.

- **Same address every time:** claim your free static domain at https://dashboard.ngrok.com/domains and run `NGROK_URL=your-name.ngrok-free.app ./share.sh`. Otherwise the address changes on each start.
- On ngrok's free plan, visitors see a one-time ngrok warning page first; they tap **Visit Site** and continue.
- **Create the admin before sharing:** first-run setup only works on the computer running the app (`http://localhost:8080/setup`), so nobody with the link can claim the admin account.
- **QR codes need a public address.** On `localhost` the QR points to your own computer, which buyers can't open (the receipt page says so). Through `share.sh` the QR uses the ngrok address; with `NGROK_URL=…` it uses your fixed domain, so QR codes on printed and shared tickets keep working after a restart. You can also set it directly: `go run . -public-url https://your-domain`.
- The script runs the app with `-trust-proxy`, so login lockouts track each visitor's real IP and session cookies are marked `Secure` over HTTPS. Only use `-trust-proxy` behind ngrok or another proxy you control.
- Your computer must stay on and connected while people use it. For something permanent, run it on a small server instead.

## Files

- `main.go` — HTTP routes, full-page vs htmx-partial rendering, ticket/sales handlers
- `activity.go`, `activity.templ` — activity log: recording, hash chain check, the Activity page and CSV download
- `agentdocs.go`, `agentdocs.templ` — agents' legal documents and their verification
- `roles.go` — the four roles and what each may do
- `usermgmt.go`, `users.templ` — Users pages, add/edit forms with the ID document
- `identity.go` — ID documents: validation, storage, the protected download
- `approvals.go`, `approvals.templ` — approving new users and changes
- `branding.go`, `branding.templ` — App setup: name, logo, colours, currency, receipt texts and limits
- `results.go`, `results.templ` — draw results: the public main page and the entry form
- `prices.go`, `prices.templ` — price lists, their activation history and the Prices page
- `modal.go`, `modal.templ` — the add dialogs: loading, errors, confirmation and returning to the right page
- `paging.go`, `list.templ` — page links, result counts and the accent-insensitive search used by all lists
- `share.go` — QR code (SVG), public check links and the buyer's check page
- `sell.go` — sell form: reading the PIN boxes, validation, live preview, add-line and sell handlers
- `auth.go` — sessions, login/logout, first-run setup, access checks, user-management handlers
- `users.go` — User/Role types, password hashing, user store methods
- `agents.go` — Agent type, store methods and admin handlers
- `store.go` — domain types (Game, Ticket, Line, Cents) and the JSON-file store
- `views.templ`, `auth.templ`, `agents.templ` — pages; the `*_templ.go` files are generated by `templ generate`
- `share.sh` — start the app behind an ngrok tunnel
- `.gitignore`, `.githooks/pre-commit`, `.github/` (CI and Dependabot), `SECURITY.md` — keeping data and secrets out of GitHub
- `static/` — `htmx.min.js` (2.0.11), `tailwind.js` (Tailwind 4 browser build), `pin.js` (PIN-box keyboard behaviour), `share.js` (draws and shares the receipt and results images), `setup.js` (live preview on the App setup page), `modal.js` (opens/closes the dialogs, shows confirmations, and moves between the 4-digit result boxes) and `fonts/` (Archivo, Kalam + licenses)

## Working with git and GitHub

- **What stays out of git:** `.gitignore` keeps the app's data out of git: `lotto.json`, `lotto-activity.jsonl`, `lotto-documents/` and `lotto-logo`, plus the built `lotto` program, zips and CSV exports. See `SECURITY.md`.
- **Pre-commit hook:** turn it on once per clone with `git config core.hooksPath .githooks`. It stops a commit that adds data files, password hashes or tokens.
- **Automatic checks:** the GitHub Actions workflow (`.github/workflows/ci.yml`) runs on every push to `main` and on every pull request. It checks that templates are generated, the code is formatted, vet passes, it builds, and there are no known vulnerabilities (govulncheck). It also checks that no data files are committed.
- **Dependency updates:** Dependabot (`.github/dependabot.yml`) opens weekly pull requests for Go module and Actions updates.
- **Generated files are committed:** the generated `*_templ.go` files are in git, so `go build` works without templ installed. After editing a `.templ` file, run `templ generate` and commit both.

## Production note

The Tailwind browser build compiles classes in the browser, which is fine for a small app. For a leaner production setup, switch to the Tailwind standalone CLI (a single binary, no Node) to generate a static CSS file.
