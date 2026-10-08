import { Upload, X } from "lucide-preact";
import { useState } from "preact/hooks";
import { upload } from "../api";
import { bytes } from "../lib";
import type { Category, IngestResult } from "../types";
import { CategorySelect } from "./docs";
import { Dialog, Field } from "./ui";

export const ACCEPT = "application/pdf,image/jpeg,image/png,image/heic,image/heif,.pdf,.heic,.heif";

/** Upload one or more files, each its own document. */
export function UploadDialog(props: {
  files: File[];
  categories: Category[];
  onClose: () => void;
  onDone: () => void;
}) {
  const [files, setFiles] = useState(props.files);
  const [title, setTitle] = useState("");
  const [category, setCategory] = useState("0");
  const [tags, setTags] = useState("");
  const [date, setDate] = useState("");
  const [family, setFamily] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [results, setResults] = useState<IngestResult[] | null>(null);
  const [error, setError] = useState("");

  const send = async (e: Event) => {
    e.preventDefault();
    setError("");
    setProgress(0);
    try {
      const res = await upload(
        files,
        {
          title: title.trim(),
          category: category === "0" ? "" : category,
          tags,
          date,
          space: family ? "family" : "private",
        },
        setProgress,
      );
      setResults(res.results);
      if (res.results.every((r) => r.status === "added" || r.status === "duplicate")) {
        props.onDone();
      }
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setProgress(null);
    }
  };

  if (results) {
    return (
      <Dialog
        title="Uploaded"
        onClose={props.onDone}
        footer={
          <>
            <span class="spacer" />
            <button class="btn btn-primary" onClick={props.onDone}>
              Done
            </button>
          </>
        }
      >
        <ResultList results={results} />
        <p class="muted">Thumbnails and text (with OCR for scans) follow in a moment.</p>
      </Dialog>
    );
  }

  return (
    <Dialog
      title={files.length > 1 ? `Upload ${files.length} files` : "Upload"}
      onClose={props.onClose}
      footer={
        <>
          <span class="spacer" />
          <button class="btn btn-ghost" onClick={props.onClose}>
            Cancel
          </button>
          <button
            class="btn btn-primary"
            form="upload-form"
            disabled={!files.length || progress !== null}
          >
            <Upload size={14} />{" "}
            {progress !== null ? `Uploading ${Math.round(progress * 100)}%` : "Upload"}
          </button>
        </>
      }
    >
      <form id="upload-form" class="form" onSubmit={send}>
        <div class="file-list">
          {files.map((f, i) => (
            <div key={i} class="file-row">
              <span class="file-name">{f.name}</span>
              <span class="muted mono">{bytes(f.size)}</span>
              <button
                type="button"
                class="icon-btn"
                aria-label={`Remove ${f.name}`}
                onClick={() => setFiles(files.filter((_, j) => j !== i))}
              >
                <X size={14} />
              </button>
            </div>
          ))}
          <label class="btn btn-small">
            {files.length ? "Add more" : "Choose files"}
            <input
              type="file"
              multiple
              accept={ACCEPT}
              hidden
              onChange={(e) => {
                setFiles([...files, ...Array.from(e.currentTarget.files ?? [])]);
                e.currentTarget.value = "";
              }}
            />
          </label>
        </div>
        <Field
          label="Title"
          hint={files.length > 1 ? "Numbered “(1 of 3)” for each file" : "Optional"}
        >
          <input
            class="input"
            value={title}
            placeholder="From the file name"
            onInput={(e) => setTitle(e.currentTarget.value)}
          />
        </Field>
        <div class="form-grid">
          <Field label="Category">
            <CategorySelect
              value={category}
              categories={props.categories}
              onChange={setCategory}
              inbox="Inbox (sort later)"
            />
          </Field>
          <Field label="Date" hint="Blank: from the name, or today">
            <input
              class="input"
              type="date"
              value={date}
              onInput={(e) => setDate(e.currentTarget.value)}
            />
          </Field>
        </div>
        <Field label="Tags" hint="Comma-separated">
          <input
            class="input"
            value={tags}
            placeholder="car, renewal"
            onInput={(e) => setTags(e.currentTarget.value)}
          />
        </Field>
        <SpaceToggle family={family} onChange={setFamily} />
        {error && <div class="form-error">{error}</div>}
      </form>
    </Dialog>
  );
}

export function SpaceToggle(props: { family: boolean; onChange: (family: boolean) => void }) {
  return (
    <Field label="Who sees it" hint={props.family ? "Everyone with an account" : "Only you"}>
      <div class="seg">
        <button
          type="button"
          class={!props.family ? "active" : ""}
          onClick={() => props.onChange(false)}
        >
          Private
        </button>
        <button
          type="button"
          class={props.family ? "active" : ""}
          onClick={() => props.onChange(true)}
        >
          Family
        </button>
      </div>
    </Field>
  );
}

const STATUS_TEXT = {
  added: "Saved",
  duplicate: "Already there",
  skipped: "Skipped",
  failed: "Failed",
};

export function ResultList(props: { results: IngestResult[] }) {
  return (
    <div class="list">
      {props.results.map((r, i) => (
        <div key={i} class="list-row">
          <span
            class={`dot ${r.status === "added" ? "good" : r.status === "duplicate" ? "" : "bad"}`}
          />
          <span class="list-main list-link">
            {r.document && r.status !== "failed" ? (
              <a class="list-title link" href={`/documents/${r.document.id}`}>
                {r.name}
              </a>
            ) : (
              <span class="list-title">{r.name}</span>
            )}
            <span class="list-sub">
              {STATUS_TEXT[r.status]}
              {r.message && r.status !== "added" ? `: ${r.message}` : ""}
            </span>
          </span>
        </div>
      ))}
    </div>
  );
}
