import { Camera, Check, FileUp, FolderUp, Plus } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api, upload } from "../api";
import { useData } from "../hooks";
import { plural } from "../lib";
import { navigate } from "../router";
import type { Doc, IngestResult, User } from "../types";
import { ACCEPT, CategorySelect, ResultList, SpaceToggle } from "./docs";
import { ErrorNote, Field, useAction } from "./ui";

// Files dropped on another page, picked up when this one opens.
let handedOver: File[] = [];

/** Opens the Add page and saves these files straight away. */
export function addFiles(files: File[]) {
  handedOver = files;
  navigate("/add");
}

/**
 * Adding a document: take a photo or choose a file, and it's saved at
 * once. Then, if they like, a name, what it is and who sees it.
 */
export function AddPage(props: { user: User }) {
  const [progress, setProgress] = useState<number | null>(null);
  const [results, setResults] = useState<IngestResult[] | null>(null);
  const [error, setError] = useState("");
  const [dragging, setDragging] = useState(false);

  const send = async (files: File[]) => {
    if (!files.length) return;
    setError("");
    setResults(null);
    setProgress(0);
    try {
      setResults((await upload(files, {}, setProgress)).results);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setProgress(null);
    }
  };

  useEffect(() => {
    const files = handedOver;
    handedOver = [];
    send(files);
  }, []);

  const picked = (e: Event) => {
    const input = e.currentTarget as HTMLInputElement;
    const files = Array.from(input.files ?? []);
    input.value = "";
    send(files);
  };

  const added = (results ?? []).filter((r) => r.status === "added" && r.document);

  return (
    <div
      class={`page add-page ${dragging ? "dropping" : ""}`}
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
        send(Array.from(e.dataTransfer?.files ?? []));
      }}
    >
      <header class="page-head">
        <h1 class="page-title page-title-small">Add a document</h1>
      </header>

      {progress !== null ? (
        <div class="add-progress" role="status">
          <div class="add-progress-text">Saving… {Math.round(progress * 100)}%</div>
          <progress class="progress" max={1} value={progress} aria-label="Saving" />
        </div>
      ) : results ? (
        <Saved results={results} added={added} onAnother={() => setResults(null)} />
      ) : (
        <>
          <div class="add-choices">
            <label class="add-choice add-camera">
              <Camera size={34} />
              <span>
                <span class="add-choice-title">Take a photo</span>
                <span class="add-choice-sub">Of a paper you have with you</span>
              </span>
              <input type="file" accept="image/*" capture="environment" hidden onChange={picked} />
            </label>
            <label class="add-choice">
              <FileUp size={34} />
              <span>
                <span class="add-choice-title">Choose a file</span>
                <span class="add-choice-sub">
                  A PDF or photo already on this device. You can pick more than one.
                </span>
              </span>
              <input type="file" accept={ACCEPT} multiple hidden onChange={picked} />
            </label>
          </div>
          <p class="muted add-drop-hint">Or drop files anywhere on this page.</p>
          {props.user.admin && (
            <a class="add-import" href="/import">
              <FolderUp size={18} /> A whole folder? Import it instead
            </a>
          )}
        </>
      )}
      {error && <ErrorNote>Couldn't save: {error}</ErrorNote>}
    </div>
  );
}

/** What was saved, and the few details worth asking for. */
function Saved(props: { results: IngestResult[]; added: IngestResult[]; onAnother: () => void }) {
  const { results, added } = props;
  const docs = added.map((r) => r.document!);
  const one = docs.length === 1 ? docs[0] : null;
  const others = results.filter((r) => r.status !== "added");
  const cats = useData(api.categories);
  const [title, setTitle] = useState(one?.title ?? "");
  const [category, setCategory] = useState("0");
  const [family, setFamily] = useState(false);
  const { busy, error, run } = useAction();

  const done = () =>
    run(async () => {
      const changed =
        (one && title.trim() && title.trim() !== one.title) || category !== "0" || family;
      const saved: Doc[] = [];
      for (const d of docs) {
        if (!changed) {
          saved.push(d);
          continue;
        }
        saved.push(
          await api.updateDocument({
            ...d,
            title: one && title.trim() ? title.trim() : d.title,
            category_id: Number(category),
            family,
          }),
        );
      }
      navigate(saved.length === 1 ? `/documents/${saved[0].id}` : "/");
    });

  return (
    <>
      {docs.length > 0 && (
        <div class="add-saved" role="status">
          <div class="add-saved-title">
            <Check size={22} /> Saved
          </div>
          <div>
            {one
              ? `“${one.title}” is in your documents.`
              : `${plural(docs.length, "document")} added.`}{" "}
            You can add details now, or later.
          </div>
        </div>
      )}
      {others.length > 0 && <ResultList results={others} />}

      {docs.length > 0 && (
        <form
          class="form form-narrow"
          onSubmit={(e) => {
            e.preventDefault();
            done();
          }}
        >
          {one && (
            <Field label="What's it called?">
              <input
                class="input"
                value={title}
                onFocus={(e) => e.currentTarget.select()}
                onInput={(e) => setTitle(e.currentTarget.value)}
              />
            </Field>
          )}
          <Field label="What is it?" hint="Optional">
            <CategorySelect
              value={category}
              categories={cats.data ?? []}
              onChange={setCategory}
              inbox="Not sure yet"
            />
          </Field>
          <SpaceToggle family={family} onChange={setFamily} />
          {error && <div class="form-error">{error}</div>}
          <div class="add-buttons">
            <button class="btn btn-primary btn-big" disabled={busy}>
              <Check size={18} /> Done
            </button>
            <button type="button" class="btn btn-big" onClick={props.onAnother}>
              <Plus size={18} /> Add another
            </button>
          </div>
          <p class="muted">You can change all of this later.</p>
        </form>
      )}
      {docs.length === 0 && (
        <div class="add-buttons">
          <button type="button" class="btn btn-primary btn-big" onClick={props.onAnother}>
            <Plus size={18} /> Try another
          </button>
        </div>
      )}
    </>
  );
}
