package peertubeimport

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// This file carries the INSTANCE's category taxonomy: not a per-video field but
// the list of categories the source instance offers, which every imported
// video's category id is an index into.
//
// It exists because the import already carries those ids. Vidra's built-in list
// matches PeerTube's stock 1–18 precisely so that carrying them works — but a
// PeerTube instance can replace that taxonomy wholesale
// (peertube-plugin-categories deletes the stock entries and adds its own at
// higher ids), and importing such an instance leaves every video pointing at an
// id that validates against nothing and renders as no category at all. The
// taxonomy is the missing half of a field the importer was already carrying.
//
// It runs FIRST, before users and videos. It has no parents to wait for, and a
// video's category id is only meaningful once the taxonomy that defines it is in
// place.

// KindCategoryTaxonomy's ledger key. The taxonomy is ONE thing per instance, so
// the key is fixed rather than derived from the plugin row: a source that spells
// its plugin's name differently, or reinstalls it, must still line up with what
// an earlier run recorded — that memory is the whole clobber guard.
const taxonomyLedgerID = "instance"

// taxonomyAction is what a run decided to do with the instance taxonomy.
type taxonomyAction int

const (
	// taxonomyWrite — the setting is the import's to write: either nothing is
	// stored and the import has never written one, or what is stored is exactly
	// what the import wrote last time and the source has since moved.
	taxonomyWrite taxonomyAction = iota
	// taxonomyUpToDate — what is stored already says what the source says.
	taxonomyUpToDate
	// taxonomyOperatorOwned — a taxonomy is configured that the import did not
	// write. It is a human's and is left exactly as it is.
	taxonomyOperatorOwned
	// taxonomyCleared — the import wrote a taxonomy and it is no longer there.
	// Somebody reset the key, which is a decision, not a gap to refill.
	taxonomyCleared
	// taxonomyBuiltins — the source's taxonomy IS Vidra's built-in list. No
	// override is written: an override that restates the built-ins buys nothing
	// and turns a shipped list into a frozen copy of it.
	taxonomyBuiltins
	// taxonomyEmpty — the source's plugin deletes everything and adds nothing.
	// There is no taxonomy to carry, and an empty override means "use the
	// built-ins" anyway.
	taxonomyEmpty
	// taxonomyUnsupported — the folded taxonomy cannot be stored in the setting.
	taxonomyUnsupported
)

// taxonomyTarget is the destination's side of the decision: what the setting
// currently holds (and whether it is overridden at all), and the value the
// import itself last applied ("" = it has never written one).
type taxonomyTarget struct {
	current     string
	hasOverride bool
	applied     string
}

// decideTaxonomy answers the only question that matters on a scheduled re-run:
// is this setting still the import's to update?
//
// The operator runs this import repeatedly against a source that keeps changing
// until cutover, so "write it once and never again" would stop carrying the
// source's taxonomy after the first run. "Write it every time" would silently
// undo an operator's edit every night. The ledger's memory of the exact value
// the import applied is what makes the third answer possible: the import updates
// a value it recognises as its own, and touches nothing else.
func decideTaxonomy(desired string, t taxonomyTarget) taxonomyAction {
	switch {
	case !t.hasOverride && t.applied == "":
		return taxonomyWrite // first import; the instance is still on the built-ins
	case !t.hasOverride:
		return taxonomyCleared // the import wrote one and somebody removed it
	case t.current == desired:
		return taxonomyUpToDate
	case t.applied != "" && t.current == t.applied:
		return taxonomyWrite // the import's own earlier write; the source has moved
	default:
		return taxonomyOperatorOwned
	}
}

// taxonomyPlan is one run's decision about the instance taxonomy, shared by the
// dry run (which reports it) and the run (which applies it).
type taxonomyPlan struct {
	action  taxonomyAction
	value   string // the canonical setting value the source describes
	applied string // what the ledger says the import last applied
	count   int    // categories in the effective set
	reason  string // safe detail for an unsupported plan
}

// categoryTaxonomyPlan reads the source's taxonomy and decides what to do with
// it. A nil plan means the source defines no custom taxonomy — the common case,
// and the one where Vidra's built-in list simply stands. It writes NOTHING.
func (im *Importer) categoryTaxonomyPlan(ctx context.Context) (*taxonomyPlan, error) {
	tax, ok, err := im.src.CategoryTaxonomy(ctx)
	if err != nil || !ok {
		return nil, err
	}
	// The ledger memory is read FIRST, before any outcome can return early: every
	// path that ends in a ledger write passes it back, and a path that read no
	// memory would write "" over it — quietly costing the import the ability to
	// recognise its own earlier value on a later run.
	applied, err := im.ledgerApplied(ctx, KindCategoryTaxonomy, taxonomyLedgerID)
	if err != nil {
		return nil, err
	}
	desired := foldCategories(video.Categories, tax)
	p := &taxonomyPlan{count: len(desired), applied: applied}
	switch {
	case len(desired) == 0:
		p.action = taxonomyEmpty
		return p, nil
	case sameCategories(desired, video.Categories):
		p.action = taxonomyBuiltins
		return p, nil
	}
	// Formatted and validated through the settings package itself, so the stored
	// value is byte-for-byte what the admin API would have written and cannot be
	// a value the admin UI would then refuse to save.
	p.value = instancesettings.FormatList(categoryEntries(desired))
	if err := instancesettings.Validate(instancesettings.KeyInstanceCustomCategories, p.value); err != nil {
		p.action, p.reason = taxonomyUnsupported, safeErr(err)
		return p, nil
	}
	current, hasOverride, err := im.currentSetting(ctx, instancesettings.KeyInstanceCustomCategories)
	if err != nil {
		return nil, err
	}
	p.action = decideTaxonomy(p.value, taxonomyTarget{current: current, hasOverride: hasOverride, applied: applied})
	return p, nil
}

// importCategoryTaxonomy carries the source instance's category taxonomy into
// instance_custom_categories, which REPLACES Vidra's built-in list when set.
//
// What it will not do, in either direction: it never writes an override for a
// source that has none (most PeerTube instances run the stock list, and an
// override restating it would freeze a shipped list), and it never REMOVES one
// either — a source that drops its plugin mid-migration leaves the last carried
// taxonomy standing, because the videos already imported still carry its ids.
func (im *Importer) importCategoryTaxonomy(ctx context.Context, r *Report) error {
	c := r.count(KindCategoryTaxonomy)
	p, err := im.categoryTaxonomyPlan(ctx)
	if err != nil {
		return err
	}
	if p == nil {
		r.Deferred = append(r.Deferred, taxonomyAbsentNote)
		return nil
	}
	switch p.action {
	case taxonomyWrite:
		if err := im.applyCategoryTaxonomy(ctx, p); err != nil {
			im.markFailed(ctx, KindCategoryTaxonomy, taxonomyLedgerID, safeErr(err))
			c.Failed++
			im.logger.WarnContext(ctx, "peertube import: instance category taxonomy failed", "error", err)
			return nil
		}
		c.Imported++
		return nil
	case taxonomyEmpty, taxonomyUnsupported:
		if err := im.recordTaxonomy(ctx, "unsupported", p.note(), p.applied); err != nil {
			return err
		}
		c.Unsupported++
	default:
		// Up to date, operator-owned, cleared, or already-the-built-ins. All four
		// write nothing — and the ledger's memory of an earlier write is passed
		// back unchanged, so a skip never costs the import the ability to
		// recognise its own value on the next run.
		if err := im.recordTaxonomy(ctx, "skipped", p.note(), p.applied); err != nil {
			return err
		}
		c.Skipped++
	}
	// A run that decided to leave the target's taxonomy alone says so in the
	// report, in the same words the dry run used. A divergence nobody is told
	// about is the failure mode this whole pass is guarding against.
	if note, ok := p.conflict(); ok {
		r.addConflict(note)
	}
	return nil
}

// applyCategoryTaxonomy writes the setting and the ledger memory in ONE
// transaction, so a crash between them cannot leave a taxonomy the next run does
// not recognise as its own (and would therefore refuse to update).
func (im *Importer) applyCategoryTaxonomy(ctx context.Context, p *taxonomyPlan) error {
	if err := im.withTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.UpsertInstanceSetting(ctx, sqlcgen.UpsertInstanceSettingParams{
			Key:   instancesettings.KeyInstanceCustomCategories,
			Value: p.value,
			// updated_by stays NULL. The import is not an admin, and stamping one
			// would credit a person with a write they did not make.
		}); err != nil {
			return err
		}
		return q.UpsertImportLedgerApplied(ctx, sqlcgen.UpsertImportLedgerAppliedParams{
			EntityKind:   KindCategoryTaxonomy,
			SourceID:     taxonomyLedgerID,
			Status:       "done",
			Note:         p.note(),
			AppliedValue: p.value,
		})
	}); err != nil {
		return err
	}
	// The running server holds the settings overlay in memory and only reloads it
	// after its OWN writes. Without this the carried taxonomy is in the database
	// and not in effect until the next restart — the silent half-success this
	// pass exists to prevent. A failure here is logged, not fatal: the write
	// itself committed.
	if im.reloadSettings != nil {
		if err := im.reloadSettings(ctx); err != nil {
			im.logger.WarnContext(ctx, "peertube import: instance settings cache not reloaded after writing the category taxonomy", "error", err)
		}
	}
	return nil
}

// recordTaxonomy upserts the taxonomy ledger row, preserving the applied-value
// memory it is given.
func (im *Importer) recordTaxonomy(ctx context.Context, status, note, applied string) error {
	return im.q.UpsertImportLedgerApplied(ctx, sqlcgen.UpsertImportLedgerAppliedParams{
		EntityKind:   KindCategoryTaxonomy,
		SourceID:     taxonomyLedgerID,
		Status:       status,
		Note:         note,
		AppliedValue: applied,
	})
}

// planCategoryTaxonomy adds the taxonomy to a dry-run plan. Planned counts what
// the import would WRITE, so it is 1 only when the setting is the import's to
// write; the outcomes that leave the setting alone are surfaced as conflict
// notes instead, which is where an operator looks for "what would you not do".
func (im *Importer) planCategoryTaxonomy(ctx context.Context, r *Report) error {
	p, err := im.categoryTaxonomyPlan(ctx)
	if err != nil {
		return err
	}
	if p == nil {
		r.Deferred = append(r.Deferred, taxonomyAbsentNote)
		return nil
	}
	if p.action == taxonomyWrite {
		r.count(KindCategoryTaxonomy).Planned = 1
	}
	if note, ok := p.conflict(); ok {
		r.addConflict(note)
	}
	return nil
}

const taxonomyAbsentNote = "instance category taxonomy (this source defines no custom categories, so Vidra's built-in list stands)"

// note renders the SAFE ledger note for a decision — counts and explanations
// only, never a category label or any other source content.
func (p *taxonomyPlan) note() string {
	switch p.action {
	case taxonomyWrite:
		return fmt.Sprintf("carried %d categories from the source", p.count)
	case taxonomyUpToDate:
		return fmt.Sprintf("already matches the source (%d categories)", p.count)
	case taxonomyOperatorOwned:
		return "left unchanged: the taxonomy configured here was not written by the import"
	case taxonomyCleared:
		return "left unchanged: the taxonomy the import wrote was cleared on this instance"
	case taxonomyBuiltins:
		return "source taxonomy matches the built-in list; no override written"
	case taxonomyEmpty:
		return "source taxonomy defines no categories"
	case taxonomyUnsupported:
		return "source taxonomy is not storable: " + p.reason
	}
	return ""
}

// conflict returns the operator-facing report note for a decision that leaves
// the target's taxonomy alone, so a dry run says so before the run does.
func (p *taxonomyPlan) conflict() (string, bool) {
	switch p.action {
	case taxonomyOperatorOwned:
		return "instance category taxonomy is already customised here and was not written by the import; it is left unchanged (the import only ever updates its own value)", true
	case taxonomyCleared:
		return "instance category taxonomy was cleared here after the import wrote it; it is left unchanged", true
	case taxonomyEmpty:
		return "the source's category plugin deletes every category and adds none; the built-in taxonomy stands", true
	case taxonomyUnsupported:
		return "the source's category taxonomy cannot be stored (" + p.reason + "); the built-in taxonomy stands", true
	}
	return "", false
}

// currentSetting reads one instance-setting override straight from the
// database. Not through the settings service's cache: the import may be running
// in a process whose cache was loaded before this run started.
func (im *Importer) currentSetting(ctx context.Context, key string) (string, bool, error) {
	row, err := im.q.GetInstanceSetting(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Value, true, nil
}

// ledgerApplied reads the value the import last applied for a single-value
// entity. "" (no row, or a row from a run that wrote nothing) means the import
// has never written this setting.
func (im *Importer) ledgerApplied(ctx context.Context, kind, sourceID string) (string, error) {
	row, err := im.q.GetImportLedgerEntry(ctx, sqlcgen.GetImportLedgerEntryParams{EntityKind: kind, SourceID: sourceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return row.AppliedValue, nil
}
