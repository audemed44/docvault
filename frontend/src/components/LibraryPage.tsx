import { Check, Plus, Search, Sparkles, X } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { bytes, plural } from "../lib";
import type { Doc, Facets, Filter, User } from "../types";
import { addFiles } from "./AddPage";
import { DocThumb, docMeta, ExpiryChip, FamilyMark, Snippet, StatusChip } from "./docs";
import { Empty, ErrorNote, Figure, useAction } from "./ui";
import { Picker } from "./Picker";

const EMPTY: Filter = {
  q: "",
  space: "",
  category: "",
  tag: "",
  person: "",
  year: "",
  expiring: false,
  status: "",
  suggested: false,
  unclassified: false,
  warnings: false,
};

// Kept between visits, so coming back from a document keeps the search.
let lastFilter: Filter = EMPTY;

const PAGE = 60;

export function LibraryPage(props: { user: User; browse?: boolean }) {
  const [filter, setFilterState] = useState<Filter>(lastFilter);
  const [query, setQuery] = useState(filter.q);
  const [more, setMore] = useState<Doc[]>([]);
  const [dragging, setDragging] = useState(false);
  const [selected, setSelected] = useState<Set<number> | null>(null); // null: not selecting
  const facets = useData(api.facets, 15_000);
  const settings = useData(api.settings);

  const setFilter = (f: Filter) => {
    lastFilter = f;
    setMore([]);
    setFilterState(f);
  };
  const docs = useData(() => api.documents(filter), 0, [JSON.stringify(filter)]);

  // Search as you type, a moment after the last key.
  useEffect(() => {
    if (query === filter.q) return;
    const t = setTimeout(() => setFilter({ ...filter, q: query }), 250);
    return () => clearTimeout(t);
  }, [query]);

  // Refresh while documents are being processed.
  const busy = (docs.data?.documents ?? []).some(
    (d) => d.status === "pending" || d.status === "processing",
  );
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(docs.reload, 4000);
    return () => clearInterval(t);
  }, [busy]);

  const reloadAll = () => {
    setMore([]);
    docs.reload();
    facets.reload();
  };

  const loadMore = async () => {
    const offset = (docs.data?.documents.length ?? 0) + more.length;
    const next = await api.documents(filter, offset, PAGE);
    setMore([...more, ...next.documents]);
  };

  const all = [...(docs.data?.documents ?? []), ...more];
  const toggle = (id: number) => {
    if (!selected) return;
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  };
  const total = docs.data?.total ?? 0;
  const f = facets.data;
  const filtered = JSON.stringify({ ...filter, q: "" }) !== JSON.stringify({ ...EMPTY, q: "" });

  return (
    <div
      class={`page ${dragging ? "dropping" : ""}`}
      onDragOver={(e) => {
        if (e.dataTransfer?.types.includes("Files")) {
          e.preventDefault();
          setDragging(true);
        }
      }}
      onDragLeave={(e) => e.currentTarget === e.target && setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        const files = Array.from(e.dataTransfer?.files ?? []);
        if (files.length) addFiles(files);
      }}
    >
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Docvault · {props.user.name}</div>
        <h1 class="page-title">Library</h1>
        <div class="figures stagger">
          <Figure value={f ? f.total : "—"} label="Documents" tone="accent" />
          <button
            class="figure-btn figure-link"
            onClick={() => setFilter({ ...EMPTY, category: "none" })}
          >
            <Figure value={f ? f.inbox : "—"} label="Inbox" />
          </button>
          <button
            class="figure-btn figure-link"
            onClick={() => setFilter({ ...EMPTY, expiring: true })}
          >
            <Figure
              value={f ? f.expiring : "—"}
              label="Expiring soon"
              tone={f?.expiring ? "warn" : ""}
            />
          </button>
          {!!f?.failed && (
            <button
              class="figure-btn figure-link"
              onClick={() => setFilter({ ...EMPTY, status: "failed" })}
            >
              <Figure value={f.failed} label="Failed" tone="bad" />
            </button>
          )}
          {!!f?.warnings && (
            <button
              class="figure-btn figure-link"
              onClick={() => setFilter({ ...EMPTY, warnings: true })}
            >
              <Figure value={f.warnings} label="Partly read" tone="warn" />
            </button>
          )}
        </div>
      </header>

      <div class="library-bar">
        <label class="search">
          <Search size={16} />
          <input
            type="search"
            placeholder="Search, even the text inside"
            value={query}
            enterkeyhint="search"
            onInput={(e) => setQuery(e.currentTarget.value)}
          />
        </label>
        <a class="btn btn-primary" href="/add">
          <Plus size={15} /> Add
        </a>
      </div>

      <Filters facets={f} filter={filter} onChange={setFilter} />
      {f && (
        <SuggestionBar
          facets={f}
          filter={filter}
          classifierOn={!!settings.data?.classifier}
          onFilter={setFilter}
          onChanged={reloadAll}
        />
      )}

      {docs.error && <ErrorNote>{docs.error}</ErrorNote>}

      <section class="section">
        <div class="results-head">
          <span class="eyebrow">
            {docs.data ? plural(total, "document") : "Loading"}
            {filter.q && ` matching “${filter.q}”`}
          </span>
          {(filtered || filter.q) && (
            <button
              class="link-btn"
              onClick={() => {
                setQuery("");
                setFilter(EMPTY);
              }}
            >
              Clear
            </button>
          )}
          <span class="spacer" />
          {all.length > 0 && (
            <button class="link-btn" onClick={() => setSelected(selected ? null : new Set())}>
              {selected ? "Done" : "Select"}
            </button>
          )}
        </div>
        {selected && (
          <SelectionBar
            docs={all}
            selected={selected}
            classifierOn={!!settings.data?.classifier}
            onSelect={setSelected}
            onChanged={reloadAll}
          />
        )}
        {!docs.data && <div class="loading loading-list" />}
        {docs.data && all.length === 0 && (
          <Empty>
            {f?.total === 0 ? (
              <>
                Nothing here yet. Upload a scan, drop files on this page, or{" "}
                <a class="link-btn" href="/import">
                  import a folder
                </a>
                .
              </>
            ) : (
              "No documents match."
            )}
          </Empty>
        )}
        {all.length > 0 && (
          <>
            <DocGrid docs={all} selected={selected} onToggle={toggle} />
            <DocTable docs={all} selected={selected} onToggle={toggle} />
          </>
        )}
        {all.length < total && (
          <button class="btn more-btn" onClick={loadMore}>
            Show more ({total - all.length} left)
          </button>
        )}
      </section>
    </div>
  );
}

function Filters(props: { facets: Facets | null; filter: Filter; onChange: (f: Filter) => void }) {
  const { facets: f, filter } = props;
  const set = (patch: Partial<Filter>) => props.onChange({ ...filter, ...patch });
  return (
    <div class="filters">
      <div class="seg">
        {(
          [
            ["", "All", f?.total],
            ["mine", "Mine", f?.mine],
            ["family", "Family", f?.family],
          ] as const
        ).map(([v, label, n]) => (
          <button
            key={v}
            class={filter.space === v ? "active" : ""}
            onClick={() => set({ space: v })}
          >
            {label}
            {n !== undefined && <span class="seg-count">{n}</span>}
          </button>
        ))}
      </div>
      <Picker
        label="Category"
        value={filter.category}
        options={[
          { value: "", label: "All categories" },
          { value: "none", label: "Inbox", count: f?.inbox },
          ...(f?.categories ?? [])
            .filter((c) => c.count > 0 || String(c.id) === filter.category)
            .map((c) => ({ value: String(c.id), label: c.name, count: c.count })),
        ]}
        onChange={(category) => set({ category })}
      />
      {!!f?.people?.length && (
        <Picker
          label="People"
          value={filter.person}
          options={[
            { value: "", label: "Everyone" },
            ...f.people.map((t) => ({ value: t.name, label: t.name, count: t.count })),
          ]}
          onChange={(person) => set({ person })}
        />
      )}
      {!!f?.tags.length && (
        <Picker
          label="Tags"
          value={filter.tag}
          options={[
            { value: "", label: "All tags" },
            ...f.tags.map((t) => ({ value: t.name, label: t.name, count: t.count })),
          ]}
          onChange={(tag) => set({ tag })}
        />
      )}
      {!!f && f.years.length > 1 && (
        <Picker
          label="Year"
          value={filter.year}
          options={[
            { value: "", label: "Any year" },
            ...f.years.map((y) => ({ value: y.name, label: y.name, count: y.count })),
          ]}
          onChange={(year) => set({ year })}
        />
      )}
      {filter.expiring && (
        <button class="chip chip-warn chip-button" onClick={() => set({ expiring: false })}>
          With expiry dates <X size={11} />
        </button>
      )}
      {filter.suggested && (
        <button class="chip chip-accent chip-button" onClick={() => set({ suggested: false })}>
          With suggestions <X size={11} />
        </button>
      )}
      {filter.unclassified && (
        <button class="chip chip-accent chip-button" onClick={() => set({ unclassified: false })}>
          Without suggestions <X size={11} />
        </button>
      )}
      {filter.warnings && (
        <button class="chip chip-warn chip-button" onClick={() => set({ warnings: false })}>
          Partly read <X size={11} />
        </button>
      )}
      {filter.status && (
        <button class="chip chip-bad chip-button" onClick={() => set({ status: "" })}>
          {filter.status} <X size={11} />
        </button>
      )}
    </div>
  );
}

/**
 * Suggestions waiting (review them, or apply all that match the filter),
 * and documents the classifier hasn't seen (ask for suggestions).
 */
function SuggestionBar(props: {
  facets: Facets;
  filter: Filter;
  classifierOn: boolean;
  onFilter: (f: Filter) => void;
  onChanged: () => void;
}) {
  const { facets: f, filter } = props;
  const { busy, error, run } = useAction();
  const [done, setDone] = useState("");
  if (!f.suggested && !(props.classifierOn && f.unclassified) && !done) return null;
  const scope = { ...filter, suggested: true, unclassified: false };
  return (
    <div class="note note-accent suggestion-bar">
      <Sparkles size={15} class="tone-accent" />
      <div class="suggestion-bar-text">
        {f.suggested > 0 && <span>{plural(f.suggested, "suggestion")} waiting.</span>}
        {props.classifierOn && f.unclassified > 0 && (
          <span>{plural(f.unclassified, "document")} without suggestions yet.</span>
        )}
        {done && <span class="tone-good">{done}</span>}
        {error && <span class="form-error">{error}</span>}
      </div>
      <div class="toolbar">
        {f.suggested > 0 && !filter.suggested && (
          <button
            class="btn btn-small"
            onClick={() => props.onFilter({ ...filter, suggested: true })}
          >
            Review
          </button>
        )}
        {f.suggested > 0 && filter.suggested && (
          <button
            class="btn btn-primary btn-small"
            disabled={busy}
            onClick={() =>
              run(async () => {
                if (
                  !confirm(
                    "Apply every suggestion in this list? Categories, dates and tags change; titles stay.",
                  )
                ) {
                  return;
                }
                const r = await api.applySuggestions(scope);
                setDone(`Applied ${plural(r.applied, "suggestion")}.`);
                props.onFilter({ ...filter, suggested: false });
                props.onChanged();
              })
            }
          >
            Apply all
          </button>
        )}
        {props.classifierOn && f.unclassified > 0 && (
          <button
            class="btn btn-small"
            disabled={busy}
            onClick={() =>
              run(async () => {
                if (
                  !confirm(
                    `Send the masked text of ${plural(f.unclassified, "document")} to the model for suggestions?`,
                  )
                ) {
                  return;
                }
                const r = await api.requestSuggestions({ unclassified: true });
                setDone(
                  `Asked for ${plural(r.queued, "suggestion")}; they arrive as each is read.`,
                );
                props.onChanged();
              })
            }
          >
            Get suggestions
          </button>
        )}
      </div>
    </div>
  );
}

/** The selected documents: ask for their suggestions, or apply them. */
function SelectionBar(props: {
  docs: Doc[];
  selected: Set<number>;
  classifierOn: boolean;
  onSelect: (s: Set<number>) => void;
  onChanged: () => void;
}) {
  const { selected } = props;
  const { busy, error, run } = useAction();
  const [done, setDone] = useState("");
  const ids = [...selected];
  const withSuggestion = props.docs.filter((d) => selected.has(d.id) && d.suggestion).length;
  return (
    <div class="note selection-bar">
      <span class="selection-count">{plural(selected.size, "selected", "selected")}</span>
      <button class="link-btn" onClick={() => props.onSelect(new Set(props.docs.map((d) => d.id)))}>
        All shown
      </button>
      {selected.size > 0 && (
        <button class="link-btn" onClick={() => props.onSelect(new Set())}>
          None
        </button>
      )}
      <span class="spacer" />
      {done && <span class="tone-good">{done}</span>}
      {error && <span class="form-error">{error}</span>}
      <div class="toolbar">
        {props.classifierOn && (
          <button
            class="btn btn-small"
            disabled={busy || !selected.size}
            onClick={() =>
              run(async () => {
                const r = await api.requestSuggestions({}, ids);
                setDone(`Asked for ${plural(r.queued, "suggestion")}.`);
                props.onChanged();
              })
            }
          >
            <Sparkles size={13} /> Get suggestions
          </button>
        )}
        <button
          class="btn btn-primary btn-small"
          disabled={busy || !withSuggestion}
          onClick={() =>
            run(async () => {
              const r = await api.applySuggestions({}, ids);
              setDone(`Applied ${plural(r.applied, "suggestion")}.`);
              props.onSelect(new Set());
              props.onChanged();
            })
          }
        >
          Apply {withSuggestion ? plural(withSuggestion, "suggestion") : "suggestions"}
        </button>
      </div>
    </div>
  );
}

function SuggestedChip(props: { doc: Doc }) {
  return props.doc.suggestion ? <span class="chip chip-accent">Suggestion</span> : null;
}

interface Selectable {
  docs: Doc[];
  selected: Set<number> | null;
  onToggle: (id: number) => void;
}

/** Phones: a grid of page-one thumbnails. While selecting, a tap selects. */
function DocGrid(props: Selectable) {
  const { selected } = props;
  return (
    <div class="doc-grid">
      {props.docs.map((d) => {
        const body = (
          <>
            <DocThumb doc={d} />
            <span class="doc-card-title">{d.title}</span>
            <span class="doc-card-meta">{docMeta(d)}</span>
            <span class="chips">
              <StatusChip doc={d} />
              <SuggestedChip doc={d} />
              <ExpiryChip expires={d.expires} />
              <FamilyMark doc={d} />
            </span>
            <Snippet text={d.snippet} />
          </>
        );
        if (!selected) {
          return (
            <a key={d.id} class="doc-card" href={`/documents/${d.id}`}>
              {body}
            </a>
          );
        }
        return (
          <button
            key={d.id}
            type="button"
            class={`doc-card doc-card-select ${selected.has(d.id) ? "selected" : ""}`}
            aria-pressed={selected.has(d.id)}
            onClick={() => props.onToggle(d.id)}
          >
            <span class="select-box" aria-hidden="true">
              {selected.has(d.id) && <Check size={14} />}
            </span>
            {body}
          </button>
        );
      })}
    </div>
  );
}

/** Desktop: a table. */
function DocTable(props: Selectable) {
  const { selected } = props;
  return (
    <div class="table-wrap doc-table">
      <table class="table">
        <thead>
          <tr>
            {selected && <th />}
            <th />
            <th>Title</th>
            <th>Category</th>
            <th>Date</th>
            <th>Tags</th>
            <th class="num">Pages</th>
            <th class="num">Size</th>
          </tr>
        </thead>
        <tbody>
          {props.docs.map((d) => (
            <tr key={d.id} class={selected?.has(d.id) ? "row-selected" : ""}>
              {selected && (
                <td class="check-cell">
                  <label class="check">
                    <input
                      type="checkbox"
                      checked={selected.has(d.id)}
                      aria-label={`Select ${d.title}`}
                      onChange={() => props.onToggle(d.id)}
                    />
                  </label>
                </td>
              )}
              <td class="thumb-cell">
                <a href={`/documents/${d.id}`} tabIndex={-1}>
                  <DocThumb doc={d} class="thumb-small" />
                </a>
              </td>
              <td class="title-cell">
                <div class="title-stack">
                  <a class="row-title" href={`/documents/${d.id}`}>
                    {d.title}
                  </a>
                  <span class="chips">
                    <StatusChip doc={d} />
                    <SuggestedChip doc={d} />
                    <ExpiryChip expires={d.expires} />
                    <FamilyMark doc={d} />
                  </span>
                  <Snippet text={d.snippet} />
                </div>
              </td>
              <td>{d.category || <span class="muted">Inbox</span>}</td>
              <td class="mono">{d.doc_date}</td>
              <td class="tags-cell">{d.tags.join(", ")}</td>
              <td class="num mono">{d.pages || ""}</td>
              <td class="num mono">{bytes(d.size)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
