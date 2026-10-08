import {
  ArrowLeft,
  Download,
  ExternalLink,
  RefreshCw,
  Share2,
  Sparkles,
  Trash2,
} from "lucide-preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { api, docURL } from "../api";
import { useData, useUnsavedWarning } from "../hooks";
import { ago, bytes, expiryText, formatDate, langName, plural } from "../lib";
import { navigate } from "../router";
import type { Category, Doc, Settings, User } from "../types";
import { CategorySelect, DocThumb } from "./docs";
import { ErrorNote, Field, SectionHead, useAction } from "./ui";
import { SpaceToggle } from "./UploadDialog";

export function DocumentPage(props: { id: number; user: User }) {
  const doc = useData(() => api.document(props.id), 0, [props.id]);
  const cats = useData(api.categories);
  const settings = useData(api.settings);
  const d = doc.data;

  // Follow processing until it's done.
  const busy = d?.status === "pending" || d?.status === "processing";
  useEffect(() => {
    if (!busy) return;
    const t = setInterval(doc.reload, 2500);
    return () => clearInterval(t);
  }, [busy]);

  if (doc.error) {
    return (
      <div class="page">
        <BackLink />
        <ErrorNote>{doc.error}</ErrorNote>
      </div>
    );
  }
  if (!d) return <div class="page loading loading-page" />;
  const showClassifier =
    (!!settings.data?.classifier || !!d.classified || !!d.classify_error) &&
    d.status !== "pending" &&
    d.status !== "processing";

  return (
    <div class="page">
      <header class="page-head">
        <BackLink />
        <div class="eyebrow eyebrow-accent">
          {d.category || "Inbox"} · {d.family ? "Family" : "Private"}
          {d.added_by && ` · added by ${d.added_by}`}
        </div>
        <h1 class="page-title page-title-small doc-title">{d.title}</h1>
      </header>

      <div class="doc-layout">
        <div class="doc-side">
          <a class="doc-preview" href={docURL.file(d.id)} target="_blank" rel="noopener">
            <DocThumb doc={d} class="thumb-large" />
          </a>
          <DocActions doc={d} />
        </div>

        <div class="doc-main">
          {d.suggestion && <SuggestionNote doc={d} onDone={doc.setData} />}
          <ProcessingNote doc={d} settings={settings.data} onQueued={doc.setData} />
          <EditForm key={d.updated} doc={d} categories={cats.data ?? []} onSaved={doc.setData} />
        </div>
      </div>

      {d.status !== "pending" && d.status !== "processing" && (
        <TextSection doc={d} settings={settings.data} onQueued={doc.setData} />
      )}
      {showClassifier && (
        <ClassifierSection doc={d} settings={settings.data} onQueued={doc.setData} />
      )}
      <FileSection doc={d} index={busy ? 1 : showClassifier ? 3 : 2} />
    </div>
  );
}

function BackLink() {
  return (
    <a class="eyebrow back" href="/">
      <ArrowLeft size={13} /> Library
    </a>
  );
}

/** Open, download and share (the share sheet on iPhone). */
function DocActions(props: { doc: Doc }) {
  const d = props.doc;
  const name = `${d.title.replace(/[/\\:*?"<>|]/g, "_")}.pdf`;
  const shareable = typeof navigator.share === "function";
  // iOS only opens the share sheet straight from a tap, so the file is
  // fetched beforehand.
  const blob = useRef<Promise<Blob> | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    if (shareable && d.status === "ready" && d.size < 40 << 20) {
      blob.current = fetch(docURL.file(d.id)).then((r) => r.blob());
      blob.current.catch(() => {});
    }
  }, [d.id, d.status]);

  const share = async () => {
    setError("");
    try {
      const b = await (blob.current ?? fetch(docURL.file(d.id)).then((r) => r.blob()));
      const file = new File([b], b.type === "application/pdf" ? name : d.file_name, {
        type: b.type,
      });
      if (navigator.canShare?.({ files: [file] })) {
        await navigator.share({ files: [file], title: d.title });
      } else {
        window.location.href = docURL.file(d.id, true);
      }
    } catch (e) {
      if ((e as Error).name !== "AbortError") setError((e as Error).message);
    }
  };

  return (
    <div class="doc-actions">
      <a class="btn btn-primary" href={docURL.file(d.id)} target="_blank" rel="noopener">
        <ExternalLink size={14} /> Open
      </a>
      {shareable && (
        <button class="btn" onClick={share}>
          <Share2 size={14} /> Share
        </button>
      )}
      <a class="btn" href={docURL.file(d.id, true)} download>
        <Download size={14} /> Download
      </a>
      {error && <div class="form-error">{error}</div>}
    </div>
  );
}

function SuggestionNote(props: { doc: Doc; onDone: (d: Doc) => void }) {
  const { doc } = props;
  const s = doc.suggestion!;
  const { busy, error, run } = useAction();
  const rows: [string, string][] = [];
  if (s.title && s.title !== doc.title) rows.push(["Title", s.title]);
  if (s.category && s.category !== doc.category) rows.push(["Category", s.category]);
  if (s.tags?.length) rows.push(["Tags", s.tags.join(", ")]);
  if (s.doc_date && s.doc_date !== doc.doc_date) rows.push(["Date", formatDate(s.doc_date)]);
  if (s.expires && s.expires !== doc.expires) rows.push(["Expires", formatDate(s.expires)]);
  return (
    <div class="note note-accent suggestion">
      <div class="eyebrow eyebrow-accent">
        <Sparkles size={12} /> Suggested
      </div>
      <dl class="kv">
        {rows.map(([k, v]) => (
          <div key={k}>
            <dt>{k}</dt>
            <dd>{v}</dd>
          </div>
        ))}
      </dl>
      {error && <div class="form-error">{error}</div>}
      <div class="toolbar">
        <button
          class="btn btn-primary btn-small"
          disabled={busy}
          onClick={() => run(async () => props.onDone(await api.applySuggestion(doc.id)))}
        >
          Apply
        </button>
        <button
          class="btn btn-ghost btn-small"
          disabled={busy}
          onClick={() =>
            run(async () => {
              await api.dismissSuggestion(doc.id);
              props.onDone({ ...doc, suggestion: undefined });
            })
          }
        >
          Dismiss
        </button>
      </div>
    </div>
  );
}

function ProcessingNote(props: {
  doc: Doc;
  settings: Settings | null;
  onQueued: (d: Doc) => void;
}) {
  const { doc } = props;
  const { busy, error, run } = useAction();
  if (doc.status === "pending" || doc.status === "processing") {
    return (
      <div class="note note-accent">
        <span class="dot accent" /> Reading the document: thumbnail, text, and OCR if it's a scan.
        Long scans take a minute or two.
      </div>
    );
  }
  if (doc.status !== "failed") return null;
  return (
    <div class="note note-bad">
      <strong>Processing failed.</strong> {doc.error}
      {error && <div class="form-error">{error}</div>}
      <div class="toolbar note-actions">
        <button
          class="btn btn-small"
          disabled={busy}
          onClick={() => run(async () => props.onQueued(await api.reprocess(doc.id, false)))}
        >
          <RefreshCw size={13} /> Try again
        </button>
      </div>
    </div>
  );
}

function EditForm(props: { doc: Doc; categories: Category[]; onSaved: (d: Doc) => void }) {
  const [d, setD] = useState(props.doc);
  const [tags, setTags] = useState(props.doc.tags.join(", "));
  const { busy, error, run } = useAction();
  const edited: Doc = {
    ...d,
    tags: tags
      .split(",")
      .map((t) => t.trim())
      .filter(Boolean),
  };
  const dirty =
    JSON.stringify([edited.title, edited.category_id, edited.doc_date, edited.expires]) !==
      JSON.stringify([
        props.doc.title,
        props.doc.category_id,
        props.doc.doc_date,
        props.doc.expires,
      ]) ||
    edited.notes !== props.doc.notes ||
    edited.family !== props.doc.family ||
    edited.tags.join(",") !== props.doc.tags.join(",");
  useUnsavedWarning(dirty);

  const save = (e: Event) => {
    e.preventDefault();
    if (edited.family !== props.doc.family && !edited.family && props.doc.family) {
      if (!confirm("Move it out of Family into your own library? Others won't see it any more.")) {
        return;
      }
    }
    run(async () => props.onSaved(await api.updateDocument(edited)));
  };
  const expiry = d.expires ? expiryText(d.expires) : null;

  return (
    <form class="form doc-form" onSubmit={save}>
      <Field label="Title">
        <input
          class="input"
          value={d.title}
          onInput={(e) => setD({ ...d, title: e.currentTarget.value })}
        />
      </Field>
      <div class="form-grid">
        <Field label="Category">
          <CategorySelect
            value={String(d.category_id)}
            categories={props.categories}
            onChange={(v) => setD({ ...d, category_id: Number(v) })}
          />
        </Field>
        <Field label="Document date">
          <input
            class="input"
            type="date"
            value={d.doc_date}
            onInput={(e) => setD({ ...d, doc_date: e.currentTarget.value })}
          />
        </Field>
        <Field
          label="Expires"
          hint={
            expiry ? (
              <span class={`tone-${expiry.tone}`}>{expiry.text}</span>
            ) : (
              "Passport, policy, licence…"
            )
          }
        >
          <input
            class="input"
            type="date"
            value={d.expires}
            onInput={(e) => setD({ ...d, expires: e.currentTarget.value })}
          />
        </Field>
      </div>
      <Field label="Tags" hint="Comma-separated">
        <input class="input" value={tags} onInput={(e) => setTags(e.currentTarget.value)} />
      </Field>
      <Field label="Notes">
        <textarea
          class="input textarea"
          rows={3}
          value={d.notes}
          onInput={(e) => setD({ ...d, notes: e.currentTarget.value })}
        />
      </Field>
      <SpaceToggle family={d.family} onChange={(family) => setD({ ...d, family })} />
      {error && <div class="form-error">{error}</div>}
      <div class="toolbar">
        <button class="btn btn-primary" disabled={!dirty || busy || !d.title.trim()}>
          Save
        </button>
        {dirty && (
          <button
            type="button"
            class="btn btn-ghost"
            onClick={() => {
              setD(props.doc);
              setTags(props.doc.tags.join(", "));
            }}
          >
            Undo changes
          </button>
        )}
      </div>
    </form>
  );
}

function TextSection(props: { doc: Doc; settings: Settings | null; onQueued: (d: Doc) => void }) {
  const { doc, settings } = props;
  const text = useData(() => api.documentText(doc.id), 0, [doc.id, doc.updated, doc.status]);
  const [open, setOpen] = useState(false);
  const [lang, setLang] = useState(doc.ocr_lang || settings?.ocr_langs || "eng+hin");
  const { busy, error, run } = useAction();
  const t = text.data?.text ?? "";
  const langs = settings?.languages.length ? settings.languages : ["eng", "hin"];
  const options = Array.from(new Set([...langs, langs.join("+"), lang])).filter(Boolean);

  return (
    <section class="section">
      <SectionHead index={1} title="Text">
        <span class="muted">
          {doc.text_source === "pdf" && "From the PDF's own text layer"}
          {doc.text_source === "ocr" && `OCR · ${langName(doc.ocr_lang || "")}`}
          {!doc.text_source && "None found"}
        </span>
      </SectionHead>
      {t ? (
        <div class={`doc-text ${open ? "open" : ""}`}>
          <pre>{t}</pre>
          {!open && t.length > 600 && (
            <button class="link-btn doc-text-more" onClick={() => setOpen(true)}>
              Show all
            </button>
          )}
        </div>
      ) : (
        <p class="muted">
          {text.data ? "No text yet. Run OCR if this is a scan." : text.error || "Loading…"}
        </p>
      )}
      <div class="toolbar">
        <select
          class="input select select-auto"
          value={lang}
          aria-label="OCR language"
          onChange={(e) => setLang(e.currentTarget.value)}
        >
          {options.map((l) => (
            <option key={l} value={l}>
              {langName(l)}
            </option>
          ))}
        </select>
        <button
          class="btn btn-small"
          disabled={busy}
          onClick={() => run(async () => props.onQueued(await api.reprocess(doc.id, true, lang)))}
        >
          <RefreshCw size={13} /> {doc.text_source === "ocr" ? "Run OCR again" : "Run OCR"}
        </button>
        {error && <span class="form-error">{error}</span>}
      </div>
    </section>
  );
}

/** The classifier: when it last looked, asking again, and exactly what it was sent. */
function ClassifierSection(props: {
  doc: Doc;
  settings: Settings | null;
  onQueued: (d: Doc) => void;
}) {
  const { doc, settings } = props;
  const [shown, setShown] = useState<string | null>(null);
  const { busy, error, run } = useAction();
  const on = !!settings?.classifier;
  return (
    <section class="section">
      <SectionHead index={2} title="Suggestions">
        <span class="muted">
          {settings?.classifier.startsWith("llm:")
            ? settings.classifier.slice(4)
            : on
              ? "Classifier"
              : "Off"}
        </span>
      </SectionHead>
      {doc.classify_error && (
        <div class="note note-warn">Couldn't get a suggestion: {doc.classify_error}</div>
      )}
      <p class="muted">
        {doc.classified
          ? `Last asked ${ago(doc.classified)}${doc.suggestion ? "; the suggestion is above." : "; nothing to suggest, or it was applied."}`
          : "Not asked yet."}{" "}
        It only ever gets the masked text, never the file.
      </p>
      {shown !== null && (
        <div class="doc-text open">
          <pre>{shown || "Nothing sent yet."}</pre>
        </div>
      )}
      <div class="toolbar">
        {on && (
          <button
            class="btn btn-small"
            disabled={busy}
            onClick={() =>
              run(async () => {
                await api.requestSuggestions({}, [doc.id]);
                props.onQueued(await api.document(doc.id));
              })
            }
          >
            <Sparkles size={13} /> {doc.classified ? "Suggest again" : "Get a suggestion"}
          </button>
        )}
        <button
          class="btn btn-ghost btn-small"
          disabled={busy}
          onClick={() =>
            shown !== null
              ? setShown(null)
              : run(async () => setShown((await api.classifierInput(doc.id)).text))
          }
        >
          {shown !== null ? "Hide what it saw" : "What the classifier saw"}
        </button>
        {error && <span class="form-error">{error}</span>}
      </div>
    </section>
  );
}

function FileSection(props: { doc: Doc; index: number }) {
  const d = props.doc;
  const { busy, error, run } = useAction();
  const remove = () =>
    run(async () => {
      if (!confirm(`Delete “${d.title}” and its file? This can't be undone.`)) return;
      await api.deleteDocument(d.id);
      navigate("/");
    });
  const photo = d.mime.startsWith("image/");
  return (
    <section class="section">
      <SectionHead index={props.index} title="File" />
      <dl class="kv kv-wide">
        <div>
          <dt>Uploaded as</dt>
          <dd>{d.file_name}</dd>
        </div>
        <div>
          <dt>Type</dt>
          <dd>
            {photo ? `Photo (${d.mime.slice(6).toUpperCase()}), shown as a PDF` : "PDF"}
            {d.pages > 0 && ` · ${plural(d.pages, "page")}`} · {bytes(d.size)}
          </dd>
        </div>
        <div>
          <dt>Added</dt>
          <dd>
            {new Date(d.created).toLocaleString()} {d.added_by && `by ${d.added_by}`}
          </dd>
        </div>
        <div>
          <dt>SHA-256</dt>
          <dd class="mono hash">{d.sha256}</dd>
        </div>
      </dl>
      {error && <ErrorNote>{error}</ErrorNote>}
      <div class="toolbar">
        <a class="btn btn-small" href={docURL.original(d.id)} download>
          <Download size={13} /> {photo ? "Original photo" : "Original file"}
        </a>
        <span class="spacer" />
        <button class="btn btn-danger btn-small" onClick={remove} disabled={busy}>
          <Trash2 size={13} /> Delete
        </button>
      </div>
    </section>
  );
}
