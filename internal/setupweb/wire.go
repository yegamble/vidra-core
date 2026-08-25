package setupweb

// The wire types: everything that crosses between the embedded page and this
// server, in one file so the whole surface can be read at once.
//
// TWO RULES SHAPE ALL OF IT.
//
//  1. NO SECRET VALUE EVER GOES OUT. Requests carry secrets — that is what a
//     setup wizard is for — but nothing this server SENDS may contain one. A
//     secret the deployment already has is reported by NAME and by presence
//     ("set; leave blank to keep"), exactly as the terminal's report() and its
//     masked prompts do. That is why the request form and the seeded answers are
//     different types rather than the same one round-tripped: a shared struct is
//     one careless field away from echoing STORAGE_S3_SECRET_KEY back into a
//     form input, and a struct that cannot hold the value cannot leak it.
//
//  2. NO PATH CROSSES THE WIRE. The four files the wizard reads and writes are
//     fixed in Options from the command line. Nothing here names a file, so a
//     request cannot aim the writer anywhere.

// Form is the operator's answers, as the page posts them for Review, Apply and
// the Answers assembly. Every field maps to exactly one setup.Answers field —
// see BuildAnswers, which is the single translation and the thing the parity
// test pins against the terminal interview.
//
// The three POINTERS are the fields where "unanswered" and "off" are different
// answers, and they are pointers for the reason setup.Answers documents: a bool
// cannot say "leave it as it is", so a page that omitted the block would silently
// turn every optional component off on a re-run.
type Form struct {
	TLSMode      string `json:"tls_mode"`
	Domain       string `json:"domain"`
	AcmeEmail    string `json:"acme_email"`
	InstanceName string `json:"instance_name"`
	ReleaseTag   string `json:"release_tag"`
	CoreTag      string `json:"core_tag"`
	UserTag      string `json:"user_tag"`
	SearchTag    string `json:"search_tag"`

	Storage  string   `json:"storage"`
	S3       S3Form   `json:"s3"`
	Database ConnForm `json:"database"`
	Redis    ConnForm `json:"redis"`

	Features     *FeatureForm      `json:"features"`
	Mail         *MailForm         `json:"mail"`
	Registration *RegistrationForm `json:"registration"`
	PeerTube     *PeerTubeForm     `json:"peertube"`
}

// PeerTubeForm is the migration-source block, and it is the FOURTH pointer for
// the third pointer's reason: a page that omitted it must mean "leave the source
// exactly as it is", not "turn the import off". A re-run about the domain sends
// nothing here and a migration in flight survives it.
//
// SourceURL and S3.SecretKey are write-only, like every other secret on this
// wire: posted when the operator types one, empty when they leave the field
// alone, which the engine already reads as "keep what the file has".
type PeerTubeForm struct {
	Enabled     bool   `json:"enabled"`
	SourceURL   string `json:"source_url"`
	Storage     string `json:"storage"`
	LocalRoot   string `json:"local_root"`
	S3          S3Form `json:"s3"`
	MediaMode   string `json:"media_mode"`
	ConflictPol string `json:"conflict_policy"`
}

// S3Form is the object-store block. SecretKey is WRITE-ONLY: it is posted when
// the operator types one and is empty when they leave the field alone, which the
// engine already reads as "keep what the file has".
type S3Form struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

// ConnForm is one external-datastore answer. URL is write-only for the same
// reason the terminal prompts for it with echo off: a managed connection string
// carries the password inside it.
type ConnForm struct {
	Mode string `json:"mode"`
	URL  string `json:"url"`
}

type FeatureForm struct {
	Scan     bool `json:"scan"`
	Captions bool `json:"captions"`
	Media    bool `json:"media"`
	Otel     bool `json:"otel"`
	IPFS     bool `json:"ipfs"`
}

// MailForm is the SMTP block. Password is write-only. A nil *MailForm is the
// wizard's "no SMTP", and it means what declining the terminal's SMTP question
// means: applyMailRule turns MAIL_ENABLED off rather than writing a file the api
// refuses to boot from.
type MailForm struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}

type RegistrationForm struct {
	Enabled         bool `json:"enabled"`
	RequireApproval bool `json:"require_approval"`
}

// ---------------------------------------------------------------------------
// GET /api/state — the Welcome step's state detection.

type StateResponse struct {
	// Template and Output are the two paths this run is pointed at, echoed so the
	// page can say which deployment it is about. They are OUTPUT only: the page
	// cannot change them (see the file header).
	Template string `json:"template"`
	Output   string `json:"output"`
	// OutputExists is the difference between a first install and a re-run, and it
	// is what makes the Review step demand an overwrite acknowledgement.
	OutputExists bool `json:"output_exists"`

	// Seed is every answer as it stands today — the existing file's value, else
	// the template's, and never a placeholder. It is what the form fields open
	// with, so an operator re-running the wizard to change one thing presses
	// through the rest.
	Seed Seed `json:"seed"`

	// Secrets are the variable NAMES the existing file already holds a value
	// for. They are shown so an operator can see their KEKs will survive the
	// re-run; the values are not here and never will be.
	Secrets []string `json:"secrets"`

	// DeployCommand is what to type for the "I'll run it myself" path.
	DeployCommand string `json:"deploy_command"`
}

// Seed is the state's answer block. It is Form with every secret replaced by a
// boolean saying whether one is already on file — see rule 1 at the top.
type Seed struct {
	TLSMode      string `json:"tls_mode"`
	Domain       string `json:"domain"`
	AcmeEmail    string `json:"acme_email"`
	InstanceName string `json:"instance_name"`
	ReleaseTag   string `json:"release_tag"`

	Storage string `json:"storage"`
	S3      S3Seed `json:"s3"`

	Database ConnSeed `json:"database"`
	Redis    ConnSeed `json:"redis"`

	Features FeatureForm `json:"features"`

	// MailConfigured is the terminal's own test for whether SMTP is working
	// today: enabled AND a host to send through. The template's MAIL_ENABLED=true
	// beside a blank SMTP_HOST is a question, not a configuration.
	MailConfigured bool     `json:"mail_configured"`
	Mail           MailSeed `json:"mail"`

	Registration RegistrationForm `json:"registration"`

	PeerTube PeerTubeSeed `json:"peertube"`
}

// PeerTubeSeed is the migration block as it stands today, with both secrets
// reduced to presence — the source DSN and the source object store's secret key
// are never sent back to the page. The three enumerated answers carry the
// CODE's defaults when the file says nothing, so the wizard's dropdowns and the
// interview's brackets open on the same value.
type PeerTubeSeed struct {
	Enabled      bool   `json:"enabled"`
	SourceURLSet bool   `json:"source_url_set"`
	Storage      string `json:"storage"`
	LocalRoot    string `json:"local_root"`

	S3Endpoint     string `json:"s3_endpoint"`
	S3Region       string `json:"s3_region"`
	S3Bucket       string `json:"s3_bucket"`
	S3AccessKey    string `json:"s3_access_key"`
	S3SecretKeySet bool   `json:"s3_secret_key_set"`

	MediaMode   string `json:"media_mode"`
	ConflictPol string `json:"conflict_policy"`
}

// S3Seed echoes the endpoint, region, bucket and access key — the same four the
// terminal interview offers back as plain defaults — and reports the secret key
// only as present or absent.
type S3Seed struct {
	Endpoint     string `json:"endpoint"`
	Region       string `json:"region"`
	Bucket       string `json:"bucket"`
	AccessKey    string `json:"access_key"`
	SecretKeySet bool   `json:"secret_key_set"`

	// CredentialsAnswered is the storage default's rule, computed by the engine:
	// with <...> placeholders still in the key slots, an s3 answer cannot pass
	// Check, so the wizard offers local instead. Same test as the terminal's.
	CredentialsAnswered bool `json:"credentials_answered"`
}

type ConnSeed struct {
	Mode   string `json:"mode"`
	URLSet bool   `json:"url_set"`
}

type MailSeed struct {
	Host        string `json:"host"`
	Port        string `json:"port"`
	Username    string `json:"username"`
	From        string `json:"from"`
	PasswordSet bool   `json:"password_set"`
}

// ---------------------------------------------------------------------------
// POST /api/validate — one field, through the engine's own validator.

type ValidateRequest struct {
	Field   string          `json:"field"`
	Value   string          `json:"value"`
	Context ValidateContext `json:"context"`
}

// ValidateContext is what a field's validator needs BESIDES its own value. Only
// the TLS mode qualifies today, and it is load-bearing: the mode decides which
// scheme a domain is normalised to, which is why the wizard asks it first.
type ValidateContext struct {
	TLSMode string `json:"tls_mode"`
}

type ValidateResponse struct {
	OK bool `json:"ok"`
	// Normalized is what the engine would store — https:// prepended, a mode
	// spelled canonically — so the page can show the operator what they actually
	// answered.
	Normalized string `json:"normalized"`
	// Error is the ENGINE's message with its package prefix trimmed, exactly as
	// the terminal's askValid prints it. It is never a message this package
	// invented.
	Error string `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// POST /api/check-domain — preflight's DNS report, feedback and never a gate.

type DomainReport struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// ---------------------------------------------------------------------------
// POST /api/doctor — the System Check step.

type DoctorReport struct {
	Root    string         `json:"root"`
	EnvFile string         `json:"env_file"`
	Results []DoctorResult `json:"results"`
	OK      int            `json:"ok"`
	Warn    int            `json:"warn"`
	Fail    int            `json:"fail"`
}

type DoctorResult struct {
	Check   string `json:"check"`
	Section string `json:"section"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix,omitempty"`
}

// ---------------------------------------------------------------------------
// POST /api/review and POST /api/apply.

// ApplyRequest is a Form plus the one extra answer Apply needs: the explicit
// acknowledgement that an existing env file is about to be rewritten. It is the
// wizard's spelling of `vidra setup --yes`, and it is asked in Review so the
// operator agrees to it while looking at what will change.
type ApplyRequest struct {
	Form
	Overwrite bool `json:"overwrite"`
}

// ReviewResponse is the dry run: what would be written, what would be minted,
// and EVERY problem at once. Issues is the whole list, never the first one —
// finding out about the second bad answer only after fixing the first is the
// experience this wizard exists to replace.
type ReviewResponse struct {
	OK bool `json:"ok"`
	// Issues is empty when the configuration would boot. A non-empty list is why
	// Apply is refused, and it is per-variable so a field can be pointed at.
	Issues []IssueJSON `json:"issues"`
	// Error is a generation failure that is not a per-variable issue — a missing
	// domain, an unreadable template — in the engine's own words.
	Error string `json:"error,omitempty"`

	Warnings []string    `json:"warnings"`
	Secrets  SecretsJSON `json:"secrets"`
	Files    []FileJSON  `json:"files"`
	Profiles []string    `json:"profiles"`
	Origin   string      `json:"origin"`

	// OverwriteRequired is true when the output file already exists. The page
	// turns it into the acknowledgement checkbox; the server enforces it in
	// Apply, so a page that forgot to ask still cannot rewrite the file.
	OverwriteRequired bool `json:"overwrite_required"`
}

// ApplyResponse is ReviewResponse once the files are on disk. Written names them
// in the order they were written.
type ApplyResponse struct {
	ReviewResponse
	Written []FileJSON `json:"written"`
	// ClaimURL is the owner-claim handoff the terminal's report() prints: the api
	// mints a one-time token at boot and puts it in its own log, and this is the
	// page it is redeemed on.
	ClaimURL string `json:"claim_url"`
}

type IssueJSON struct {
	Var string `json:"var,omitempty"`
	Msg string `json:"msg"`
}

// SecretsJSON is the provenance block: variable NAMES only, in the same four
// buckets the terminal's report() prints.
type SecretsJSON struct {
	Generated []string `json:"generated"`
	Preserved []string `json:"preserved"`
	Rotated   []string `json:"rotated"`
	Carried   []string `json:"carried"`
}

// FileJSON is one file the run writes: the path from Options, its mode, and the
// sentence explaining what it is for.
type FileJSON struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	What string `json:"what"`
}

// ---------------------------------------------------------------------------
// POST /api/install — SSE, and POST /api/status.

// InstallEvent is one server-sent event's payload. Line events arrive as the
// deploy prints; exactly one done event ends the stream.
type InstallEvent struct {
	Text     string `json:"text,omitempty"`
	ExitCode int    `json:"exit_code"`
	// Error is a deploy that could not be STARTED, which is a different thing
	// from one that ran and failed — the second has a log to read and the first
	// has not.
	Error string `json:"error,omitempty"`
	// Hint is what to do next when the deploy did not succeed. The TAIL is not
	// repeated in it: the page already holds every line it was sent, and sending
	// the last twenty back would be the same bytes twice.
	Hint string `json:"hint,omitempty"`
}

// StatusLine is one line of the post-install probe, in `vidra status`'s own
// shape — the containers, the api's readiness, the frontend — so an operator
// reading the Success step and an operator running the CLI see the same words.
type StatusLine struct {
	Source string `json:"source"`
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type StatusResponse struct {
	Lines []StatusLine `json:"lines"`
	// ClaimURL and LogsCommand are the two halves of the owner-claim handoff,
	// verbatim from the terminal's report(): every api restart mints a fresh
	// token, so the newest line in the log is the one that works.
	ClaimURL    string `json:"claim_url"`
	LogsCommand string `json:"logs_command"`

	// ImportURL is the migration handoff, and it is EMPTY unless the file that
	// was just written says this deployment has a PeerTube source. `vidra setup`
	// can only write that configuration down — the importer runs inside the api
	// container, which does not exist while the wizard is open — so the last
	// thing the wizard owes a migrating operator is the address it is run from.
	ImportURL string `json:"import_url,omitempty"`
}
