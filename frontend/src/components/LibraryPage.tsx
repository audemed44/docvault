import { ArrowLeft, ChevronRight, Plus, Search, Sparkles, TriangleAlert, X } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { bytes, categoryColor, daysUntil, expiryText, formatDate, plural } from "../lib";
import { navigate } from "../router";
import type { Doc, Facets, Filter, User } from "../types";
import { addFiles } from "./AddPage";
import {
  CategoryIcon,
  CategoryTag,
  DocRows,
  DocThumb,
  ExpiryChip,
  FamilyMark,
  Snippet,
  StatusChip,
} from "./docs";
import { Empty, ErrorNote, SectionHead, useAction } from "./ui";
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
let lastQuery = "";

/** Opens the full list with this filter. */
function showDocuments(f: Partial<Filter>) {
  lastFilter = { ...EMPTY, ...f };
  navigate("/documents");
}

const PAGE = 60;

/** Home (search, what's expiring, categories, the latest), or every document. */
export function LibraryPage(props: { user: User; browse?: boolean }) {
  return props.browse ? <Browse user={props.user} /> : <Home user={props.user} />;
}

/** A page that takes dropped files, handing them to the Add page. */
function useDrop() {
  const [dragging, setDragging] = useState(false);
  return {
    class: dragging ? "dropping" : "",
    onDragOver: (e: DragEvent) => {
      if (e.dataTransfer?.types.includes("Files")) {
        e.preventDefault();
        setDragging(true);
      }
    },
    onDragLeave: (e: DragEvent) => e.currentTarget === e.target && setDragging(false),
    onDrop: (e: DragEvent) => {
      e.preventDefault();
      setDragging(false);
      const files = Array.from(e.dataTransfer?.files ?? []);
      if (files.length) addFiles(files);
    },
  };
}

function SearchBox(props: { value: string; onInput: (v: string) => void }) {
  return (
    <label class="search">
      <Search size={20} />
      <input
        type="search"
        placeholder="Search for a document"
        aria-label="Search for a document, even the words inside it"
        value={props.value}
        enterkeyhint="search"
        onInput={(e) => props.onInput(e.currentTarget.value)}
      />
      {props.value && (
        <button
          type="button"
          class="search-clear"
          aria-label="Clear"
          onClick={() => props.onInput("")}
        >
          <X size={20} />
        </button>
      )}
    </label>
  );
}

function Home(props: { user: User }) {
  const [query, setQuery] = useState(lastQuery);
  const [q, setQ] = useState(lastQuery.trim());
  const facets = useData(api.facets, 15_000);
  const drop = useDrop();

  // Search as you type, a moment after the last key.
  useEffect(() => {
    lastQuery = query;
    const t = setTimeout(() => setQ(query.trim()), 250);
    return () => clearTimeout(t);
  }, [query]);

  return (
    <div
      class={`page home ${drop.class}`}
      onDragOver={drop.onDragOver}
      onDragLeave={drop.onDragLeave}
      onDrop={drop.onDrop}
    >
      <header class="page-head">
        <h1 class="page-title page-title-small">Documents</h1>
        <SearchBox value={query} onInput={setQuery} />
      </header>
      {q ? (
        <SearchResults q={q} />
      ) : (
        <>
          <ExpiryAlerts />
          {props.user.admin && facets.data && <Attention facets={facets.data} />}
          {facets.data && <TypeTiles facets={facets.data} />}
          <Recent />
        </>
      )}
    </div>
  );
}

function SearchResults(props: { q: string }) {
  const res = useData(() => api.documents({ ...EMPTY, q: props.q }), 0, [props.q]);
  const [more, setMore] = useState<Doc[]>([]);
  useEffect(() => setMore([]), [props.q]);
  const all = [...(res.data?.documents ?? []), ...more];
  const total = res.data?.total ?? 0;
  const loadMore = async () => {
    const next = await api.documents({ ...EMPTY, q: props.q }, all.length, PAGE);
    setMore([...more, ...next.documents]);
  };
  return (
    <section class="section">
      {res.error && <ErrorNote>{res.error}</ErrorNote>}
      {!res.data && <div class="loading loading-list" />}
      {res.data && (
        <div class="results-head">
          <span class="results-count">
            {total ? `${plural(total, "document")} found` : `Nothing found for “${props.q}”`}
          </span>
          <span class="spacer" />
          {total > 0 && (
            <button class="link-btn" onClick={() => showDocuments({ q: props.q })}>
              Narrow down
            </button>
          )}
        </div>
      )}
      {res.data && !total && (
        <p class="muted">
          Try fewer words, or just the start of a word. It looks inside documents too.
        </p>
      )}
      <DocRows docs={all} />
      {all.length < total && (
        <button class="btn btn-big more-btn" onClick={loadMore}>
          Show more ({total - all.length} left)
        </button>
      )}
    </section>
  );
}

/** Expiring in the next 60 days, or expired in the last 30. */
function ExpiryAlerts() {
  const list = useData(() => api.documents({ ...EMPTY, expiring: true }, 0, 500));
  const [all, setAll] = useState(false);
  const soon = (list.data?.documents ?? []).filter((d) => {
    const n = daysUntil(d.expires);
    return n >= -30 && n <= 60;
  });
  if (!soon.length) return null;
  const shown = all ? soon : soon.slice(0, 3);
  return (
    <section class="alerts" aria-label="Expiring soon">
      {shown.map((d) => {
        const e = expiryText(d.expires);
        return (
          <a key={d.id} class={`alert alert-${e.tone || "warn"}`} href={`/documents/${d.id}`}>
            <TriangleAlert size={24} class="alert-icon" />
            <span class="alert-main">
              <span class="alert-title">{d.title}</span>
              <span class="alert-text">
                {e.text}
                {e.text.startsWith("Expires in") && ` · ${formatDate(d.expires)}`}
              </span>
            </span>
            <ChevronRight size={22} class="doc-row-go" />
          </a>
        );
      })}
      {soon.length > 3 && !all && (
        <button class="btn more-btn" onClick={() => setAll(true)}>
          Show {soon.length - 3} more expiring
        </button>
      )}
    </section>
  );
}

/** For admins: documents that need a look. */
function Attention(props: { facets: Facets }) {
  const f = props.facets;
  const items: [number, string, Partial<Filter>][] = [
    [f.failed, "couldn't be read", { status: "failed" }],
    [f.warnings, "with unclear pages", { warnings: true }],
    [
      f.suggested,
      f.suggested === 1 ? "suggestion waiting" : "suggestions waiting",
      { suggested: true },
    ],
  ];
  const shown = items.filter(([n]) => n > 0);
  if (!shown.length) return null;
  return (
    <div class="note attention">
      {shown.map(([n, text, filter]) => (
        <button key={text} class="link-btn" onClick={() => showDocuments(filter)}>
          {n} {text}
        </button>
      ))}
    </div>
  );
}

function TypeTiles(props: { facets: Facets }) {
  const f = props.facets;
  if (!f.total) {
    return (
      <Empty>
        Nothing here yet.{" "}
        <a class="link-btn" href="/add">
          Add your first document
        </a>
        .
      </Empty>
    );
  }
  return (
    <section class="section">
      <SectionHead title="Look by type" />
      <div class="tiles">
        {f.categories
          .filter((c) => c.count > 0)
          .map((c) => (
            <button
              key={c.id}
              class="tile"
              style={{ "--cat": categoryColor(c.name) }}
              onClick={() => showDocuments({ category: String(c.id) })}
            >
              <CategoryIcon name={c.name} />
              <span class="tile-name">{c.name}</span>
              <span class="tile-count">{plural(c.count, "document")}</span>
            </button>
          ))}
        {f.inbox > 0 && (
          <button
            class="tile"
            style={{ "--cat": categoryColor("") }}
            onClick={() => showDocuments({ category: "none" })}
          >
            <CategoryIcon name="" inbox />
            <span class="tile-name">Not sorted yet</span>
            <span class="tile-count">{plural(f.inbox, "document")}</span>
          </button>
        )}
        <button class="tile tile-all" onClick={() => showDocuments({})}>
          <ChevronRight size={22} />
          <span class="tile-name">Everything</span>
          <span class="tile-count">{plural(f.total, "document")}</span>
        </button>
      </div>
    </section>
  );
}

function Recent() {
  const docs = useData(() => api.documents({ ...EMPTY, sort: "added" }, 0, 6));
  const list = docs.data?.documents ?? [];
  const busy = list.some((d) => d.status === "pending" || d.status === "processing");
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(docs.reload, 4000);
    return () => clearInterval(t);
  }, [busy]);
  if (!list.length) return null;
  return (
    <section class="section">
      <SectionHead title="Added lately">
        <button class="link-btn" onClick={() => showDocuments({ sort: "added" })}>
          See all
        </button>
      </SectionHead>
      <DocRows docs={list} />
    </section>
  );
}

/** What the list shows, in a few words. */
function listTitle(filter: Filter, facets: Facets | null): string {
  if (filter.category === "none") return "Not sorted yet";
  if (filter.category) {
    return facets?.categories.find((c) => String(c.id) === filter.category)?.name ?? "Documents";
  }
  if (filter.expiring) return "With an expiry date";
  if (filter.sort === "added") return "Added lately";
  return "Everything";
}

/** Every document, with search and filters. */
function Browse(props: { user: User }) {
  const [filter, setFilterState] = useState<Filter>(lastFilter);
  const [query, setQuery] = useState(filter.q);
  const [more, setMore] = useState<Doc[]>([]);
  const [selected, setSelected] = useState<Set<number> | null>(null); // null: not selecting
  const facets = useData(api.facets, 15_000);
  const settings = useData(api.settings);
  const drop = useDrop();
  const admin = props.user.admin;
  const classifierOn = !!settings.data?.classifier;

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
  const filtered =
    JSON.stringify({ ...filter, q: "", sort: "" }) !==
    JSON.stringify({ ...EMPTY, q: "", sort: "" });

  return (
    <div
      class={`page ${drop.class}`}
      onDragOver={drop.onDragOver}
      onDragLeave={drop.onDragLeave}
      onDrop={drop.onDrop}
    >
      <header class="page-head">
        <a class="back" href="/">
          <ArrowLeft size={20} /> Documents
        </a>
        <h1 class="page-title page-title-small">{listTitle(filter, f)}</h1>
      </header>

      <div class="library-bar">
        <SearchBox value={query} onInput={setQuery} />
        <a class="btn btn-primary library-add" href="/add">
          <Plus size={18} /> Add
        </a>
      </div>

      <Filters facets={f} filter={filter} onChange={setFilter} />
      {f && admin && (
        <SuggestionBar
          facets={f}
          filter={filter}
          classifierOn={classifierOn}
          onFilter={setFilter}
          onChanged={reloadAll}
        />
      )}

      {docs.error && <ErrorNote>{docs.error}</ErrorNote>}

      <section class="section">
        <div class="results-head">
          <span class="results-count">
            {docs.data ? plural(total, "document") : "Loading"}
            {filter.q && ` matching “${filter.q}”`}
          </span>
          {(filtered || filter.q) && (
            <button
              class="link-btn"
              onClick={() => {
                setQuery("");
                setFilter({ ...EMPTY, sort: filter.sort });
              }}
            >
              Clear filters
            </button>
          )}
          <span class="spacer" />
          {admin && all.length > 0 && (
            <button class="link-btn" onClick={() => setSelected(selected ? null : new Set())}>
              {selected ? "Done" : "Select"}
            </button>
          )}
        </div>
        {selected && (
          <SelectionBar
            docs={all}
            selected={selected}
            classifierOn={classifierOn}
            onSelect={setSelected}
            onChanged={reloadAll}
          />
        )}
        {!docs.data && <div class="loading loading-list" />}
        {docs.data && all.length === 0 && <Empty>No documents match.</Empty>}
        {all.length > 0 && (
          <>
            <DocRows docs={all} selected={selected} onToggle={toggle} class="doc-rows-phone" />
            <DocTable docs={all} selected={selected} onToggle={toggle} />
          </>
        )}
        {all.length < total && (
          <button class="btn btn-big more-btn" onClick={loadMore}>
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
            ["mine", "Only mine", f?.mine],
            ["family", "The family", f?.family],
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
          { value: "none", label: "Not sorted yet", count: f?.inbox },
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
          Some pages unclear <X size={11} />
        </button>
      )}
      {filter.status && (
        <button class="chip chip-bad chip-button" onClick={() => set({ status: "" })}>
          {filter.status === "failed" ? "Couldn't read" : filter.status} <X size={11} />
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
              <td>
                <CategoryTag name={d.category} />
              </td>
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
