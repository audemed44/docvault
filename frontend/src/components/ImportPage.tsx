import { FolderUp, Play, RotateCcw, Upload } from "lucide-preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { api, upload } from "../api";
import { useData } from "../hooks";
import { ago, plural } from "../lib";
import type { IngestResult, IngestStatus, User } from "../types";
import { ResultList, SpaceToggle } from "./docs";
import { Empty, ErrorNote, Figure, SectionHead, useAction } from "./ui";

const SUPPORTED = /\.(pdf|jpe?g|png|heic|heif)$/i;

interface Queued {
  file: File;
  path: string;
  status: IngestStatus | "waiting" | "uploading";
  message?: string;
  id?: number;
}

/** Reads every file in dropped folders (webkitGetAsEntry). */
async function droppedFiles(items: DataTransferItemList): Promise<{ file: File; path: string }[]> {
  const out: { file: File; path: string }[] = [];
  const walk = async (entry: FileSystemEntry | null): Promise<void> => {
    if (!entry) return;
    if (entry.isFile) {
      const file = await new Promise<File>((res, rej) =>
        (entry as FileSystemFileEntry).file(res, rej),
      );
      out.push({ file, path: entry.fullPath.replace(/^\//, "") });
    } else if (entry.isDirectory) {
      const reader = (entry as FileSystemDirectoryEntry).createReader();
      for (;;) {
        const batch = await new Promise<FileSystemEntry[]>((res, rej) =>
          reader.readEntries(res, rej),
        );
        if (!batch.length) break;
        for (const e of batch) await walk(e);
      }
    }
  };
  const entries = Array.from(items).map((i) => i.webkitGetAsEntry?.() ?? null);
  for (const e of entries) await walk(e);
  return out;
}

export function ImportPage(props: { user: User }) {
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">Bulk import</div>
        <h1 class="page-title">Import</h1>
        <p class="muted page-lede">
          Bring in a whole library, like a CamScanner export. Everything lands in Not sorted yet, to
          be sorted later: file names become titles, and dates come from the name (
          <code>CamScanner 03-15-2021 10.22</code>) or the file. Files already in Docvault are
          skipped, so running an import again is safe.
        </p>
      </header>
      <FolderUpload />
      <ServerImport user={props.user} />
    </div>
  );
}

function FolderUpload() {
  const [queue, setQueue] = useState<Queued[]>([]);
  const [family, setFamily] = useState(false);
  const [running, setRunning] = useState(false);
  const [dragging, setDragging] = useState(false);
  const stop = useRef(false);
  const queueRef = useRef(queue);
  queueRef.current = queue;

  const add = (files: { file: File; path: string }[]) => {
    const known = new Set(queueRef.current.map((q) => q.path + q.file.size));
    const next = files
      .filter((f) => !f.file.name.startsWith(".") && !known.has(f.path + f.file.size))
      .map((f): Queued =>
        SUPPORTED.test(f.file.name)
          ? { ...f, status: "waiting" }
          : { ...f, status: "skipped", message: "not a PDF or photo" },
      );
    setQueue([...queueRef.current, ...next]);
  };

  const update = (i: number, patch: Partial<Queued>) =>
    setQueue((q) => q.map((item, j) => (j === i ? { ...item, ...patch } : item)));

  // One file at a time, so a slow connection or a big scan doesn't stall
  // the rest, and stopping leaves the others waiting.
  const start = async () => {
    stop.current = false;
    setRunning(true);
    for (;;) {
      if (stop.current) break;
      const i = queueRef.current.findIndex((q) => q.status === "waiting");
      if (i < 0) break;
      const item = queueRef.current[i];
      update(i, { status: "uploading" });
      queueRef.current = queueRef.current.map((q, j) =>
        j === i ? { ...q, status: "uploading" } : q,
      );
      try {
        const res = await upload([item.file], {
          modified: item.file.lastModified,
          space: family ? "family" : "private",
        });
        const r = res.results[0];
        update(i, { status: r.status, message: r.message, id: r.document?.id });
        queueRef.current = queueRef.current.map((q, j) =>
          j === i ? { ...q, status: r.status } : q,
        );
      } catch (e) {
        update(i, { status: "failed", message: (e as Error).message });
        queueRef.current = queueRef.current.map((q, j) =>
          j === i ? { ...q, status: "failed" } : q,
        );
      }
    }
    setRunning(false);
  };

  const retry = () =>
    setQueue(queue.map((q) => (q.status === "failed" ? { ...q, status: "waiting" } : q)));

  const count = (s: Queued["status"]) => queue.filter((q) => q.status === s).length;
  const waiting = count("waiting") + count("uploading");
  const results: IngestResult[] = queue
    .filter((q) => q.status !== "waiting" && q.status !== "uploading")
    .map((q) => ({
      name: q.path,
      status: q.status as IngestStatus,
      message: q.message,
      document: q.id ? ({ id: q.id } as IngestResult["document"]) : undefined,
    }));
  const problems = results.filter((r) => r.status === "failed" || r.status === "skipped");

  return (
    <section class="section">
      <SectionHead index={1} title="From this computer" />
      <div
        class={`dropzone ${dragging ? "over" : ""}`}
        onDragOver={(e) => {
          e.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={async (e) => {
          e.preventDefault();
          setDragging(false);
          if (e.dataTransfer) add(await droppedFiles(e.dataTransfer.items));
        }}
      >
        <FolderUp size={22} />
        <span>Drop a folder here, or</span>
        <div class="toolbar">
          <label class="btn btn-small">
            Choose a folder
            <input
              type="file"
              hidden
              multiple
              {...{ webkitdirectory: "" }}
              onChange={(e) => {
                add(
                  Array.from(e.currentTarget.files ?? []).map((file) => ({
                    file,
                    path: file.webkitRelativePath || file.name,
                  })),
                );
                e.currentTarget.value = "";
              }}
            />
          </label>
          <label class="btn btn-small">
            Choose files
            <input
              type="file"
              hidden
              multiple
              onChange={(e) => {
                add(
                  Array.from(e.currentTarget.files ?? []).map((file) => ({
                    file,
                    path: file.name,
                  })),
                );
                e.currentTarget.value = "";
              }}
            />
          </label>
        </div>
      </div>

      {queue.length > 0 && (
        <>
          <div class="figures">
            <Figure
              value={count("added")}
              unit={`/${queue.length}`}
              label="Imported"
              tone="accent"
            />
            <Figure value={count("duplicate")} label="Already there" />
            <Figure
              value={count("failed") + count("skipped")}
              label="Failed or skipped"
              tone={count("failed") ? "bad" : ""}
            />
            <Figure value={waiting} label="Waiting" />
          </div>
          {!running && <SpaceToggle family={family} onChange={setFamily} />}
          <div class="toolbar">
            {running ? (
              <button class="btn" onClick={() => (stop.current = true)}>
                Stop after this file
              </button>
            ) : (
              <button class="btn btn-primary" onClick={start} disabled={!waiting}>
                <Upload size={14} /> Import {plural(waiting, "file")}
              </button>
            )}
            {!running && count("failed") > 0 && (
              <button class="btn" onClick={retry}>
                <RotateCcw size={14} /> Retry failed
              </button>
            )}
            {!running && (
              <button class="btn btn-ghost" onClick={() => setQueue([])}>
                Clear
              </button>
            )}
          </div>
          {running && (
            <progress
              class="progress"
              max={queue.length}
              value={queue.length - waiting}
              aria-label="Import progress"
            />
          )}
          {problems.length > 0 && (
            <>
              <div class="eyebrow">Failed or skipped</div>
              <ResultList results={problems} />
            </>
          )}
        </>
      )}
    </section>
  );
}

function ServerImport(props: { user: User }) {
  const report = useData(api.importStatus);
  const { busy, error, run } = useAction();
  const r = report.data;

  useEffect(() => {
    if (!r?.running) return;
    const t = setInterval(report.reload, 1500);
    return () => clearInterval(t);
  }, [r?.running]);

  return (
    <section class="section">
      <SectionHead index={2} title="From the server" />
      <p class="muted page-lede">
        For big exports, copy them onto the server instead (<code>rsync</code>) into{" "}
        <code>docvault/import/{props.user.username}/</code> next to the compose file, which is{" "}
        <code>{r?.folder ?? "/data/import/…"}</code> inside the container. The files are left there
        afterwards; delete them once you're happy.
      </p>
      {report.error && <ErrorNote>{report.error}</ErrorNote>}
      {error && <ErrorNote>{error}</ErrorNote>}
      {r && (
        <>
          <div class="toolbar">
            <button
              class="btn btn-primary"
              disabled={busy || r.running || !r.waiting}
              onClick={() => run(async () => report.setData(await api.startImport()))}
            >
              <Play size={14} /> {r.running ? "Importing…" : `Import ${plural(r.waiting, "file")}`}
            </button>
            {!r.running && (
              <button class="btn btn-ghost" onClick={report.reload}>
                Look again
              </button>
            )}
          </div>
          {r.started ? (
            <>
              <div class="figures">
                <Figure value={r.added} unit={`/${r.total}`} label="Imported" tone="accent" />
                <Figure value={r.duplicates} label="Already there" />
                <Figure
                  value={r.failed.length}
                  label="Failed"
                  tone={r.failed.length ? "bad" : ""}
                />
              </div>
              <p class="muted">
                {r.running ? "Started" : "Finished"} {ago(r.running ? r.started : r.finished)}.
              </p>
              {r.failed.length > 0 && <ResultList results={r.failed} />}
            </>
          ) : (
            !r.waiting && <Empty>The folder is empty.</Empty>
          )}
        </>
      )}
    </section>
  );
}
