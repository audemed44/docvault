import { Plus, X } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import type { Person, Settings, VocabTag } from "../types";
import { ErrorNote, Field, SectionHead, useAction } from "./ui";

/** The tags the classifier may suggest, filed under categories. */
export function TagsSection(props: { index: number }) {
  const vocab = useData(api.tagVocab);
  const unlisted = useData(api.unlistedTags);
  const cats = useData(api.categories);
  const [list, setList] = useState<VocabTag[] | null>(null);
  const [adding, setAdding] = useState<Record<number, string>>({});
  const { busy, error, run } = useAction();
  const tags = list ?? vocab.data ?? [];
  const groups = [
    ...(cats.data ?? []).map((c) => ({ id: c.id, name: c.name })),
    { id: 0, name: "Any category" },
  ];

  const add = (categoryID: number) => {
    const names = (adding[categoryID] ?? "")
      .split(",")
      .map((t) => t.trim().replace(/^#/, ""))
      .filter((t) => t && !tags.some((x) => x.name.toLowerCase() === t.toLowerCase()));
    if (!names.length) return;
    setList([...tags, ...names.map((name) => ({ name, category_id: categoryID }))]);
    setAdding({ ...adding, [categoryID]: "" });
  };

  return (
    <section class="section">
      <SectionHead index={props.index} title="Tags" />
      <p class="muted page-lede">
        The tags suggestions may use; anything else a model comes up with is dropped. Filing them
        under a category helps it choose. You can still type any tag on a document yourself.
        People's names (below) are tags too.
      </p>
      {(vocab.error || cats.error) && <ErrorNote>{vocab.error || cats.error}</ErrorNote>}
      <div class="vocab">
        {groups.map((g) => {
          const mine = tags.filter((t) => t.category_id === g.id);
          if (g.id === 0 && !mine.length && !list) return null;
          return (
            <div key={g.id} class="vocab-row">
              <span class="vocab-cat">{g.name}</span>
              <div class="chips">
                {mine.map((t) => (
                  <button
                    key={t.name}
                    class="chip chip-button"
                    title={`Remove ${t.name}`}
                    onClick={() => setList(tags.filter((x) => x !== t))}
                  >
                    {t.name} <X size={10} />
                  </button>
                ))}
                <input
                  class="vocab-add"
                  placeholder="add…"
                  aria-label={`Add a tag to ${g.name}`}
                  value={adding[g.id] ?? ""}
                  onInput={(e) => setAdding({ ...adding, [g.id]: e.currentTarget.value })}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === ",") {
                      e.preventDefault();
                      add(g.id);
                    }
                  }}
                  onBlur={() => add(g.id)}
                />
              </div>
            </div>
          );
        })}
      </div>
      {!!unlisted.data?.length && (
        <div class="unlisted">
          <span class="eyebrow">Used on documents, not on the list</span>
          <div class="chips">
            {unlisted.data
              .filter((u) => !tags.some((t) => t.name.toLowerCase() === u.name.toLowerCase()))
              .map((u) => (
                <button
                  key={u.name}
                  class="chip chip-button"
                  title={`Add ${u.name} to the list`}
                  onClick={() => setList([...tags, { name: u.name, category_id: 0 }])}
                >
                  <Plus size={10} /> {u.name} <span class="muted">{u.count}</span>
                </button>
              ))}
          </div>
          <span class="field-hint">
            Suggestions' new tags, and tags typed by hand. Adding one puts it under “Any category”;
            save to keep it.
          </span>
        </div>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      {list && (
        <div class="toolbar">
          <button
            class="btn btn-primary btn-small"
            disabled={busy}
            onClick={() =>
              run(async () => {
                vocab.setData(await api.saveTagVocab(list));
                unlisted.reload();
                setList(null);
              })
            }
          >
            Save tags
          </button>
          <button class="btn btn-ghost btn-small" onClick={() => setList(null)}>
            Undo
          </button>
        </div>
      )}
    </section>
  );
}

const splitList = (s: string) =>
  s
    .split(/[,\n]/)
    .map((x) => x.trim())
    .filter(Boolean);

/** Who's in the family, what else to hide, and a box to try the masking. */
export function MaskingSection(props: {
  index: number;
  settings: Settings;
  onSaved: (s: Settings) => void;
}) {
  const [people, setPeople] = useState<{ name: string; aliases: string }[]>(() =>
    props.settings.people.map((p) => ({ name: p.name, aliases: p.aliases.join(", ") })),
  );
  const [words, setWords] = useState(props.settings.mask_words.join("\n"));
  const [from, setFrom] = useState(props.settings.classify_from || "auto");
  const [when, setWhen] = useState(props.settings.suggest_new || "ask");
  const [sample, setSample] = useState("");
  const [masked, setMasked] = useState<string | null>(null);
  const save = useAction();
  const test = useAction();

  const edited: Person[] = people
    .filter((p) => p.name.trim())
    .map((p) => ({ name: p.name.trim(), aliases: splitList(p.aliases) }));
  const dirty =
    JSON.stringify(edited) !== JSON.stringify(props.settings.people) ||
    JSON.stringify(splitList(words)) !== JSON.stringify(props.settings.mask_words) ||
    from !== props.settings.classify_from ||
    when !== props.settings.suggest_new;
  const c = props.settings.classifier;

  return (
    <section class="section">
      <SectionHead index={props.index} title="Suggestions">
        <span class={c ? "tone-good" : "muted"}>
          {c.startsWith("llm:") ? `On · ${c.slice(4)}` : c ? "On" : "Off"}
        </span>
      </SectionHead>
      <p class="muted page-lede">
        {c ? (
          <>
            Each document's name (and its text, as set below) is masked, then sent to the model,
            which suggests a title, category, tags and dates to apply. ID numbers, phone numbers,
            emails, long numbers, labelled names, addresses and birth dates, and the people and
            words below are replaced with placeholders like <code>[pan]</code> first. Unlabelled
            names and addresses, and what the document is about, still go through.
          </>
        ) : (
          <>
            Off. Set <code>DOCVAULT_LLM_KEY</code> and <code>DOCVAULT_LLM_MODEL</code> (OpenRouter,
            or <code>DOCVAULT_LLM_URL</code> for another OpenAI-compatible API) to get suggested
            titles, categories, tags and dates. Text is masked before it's sent.
          </>
        )}
      </p>

      <div class="form form-narrow">
        <Field
          label="When"
          hint={
            when === "ask"
              ? "Nothing is sent until you ask: Get a suggestion on a document, or select documents in the library."
              : "Every new upload and import is sent once it's read."
          }
        >
          <select
            class="input select"
            value={when}
            onChange={(e) => setWhen(e.currentTarget.value as typeof when)}
          >
            <option value="ask">Only when I ask</option>
            <option value="auto">Automatically, for every new document</option>
          </select>
        </Field>
        <Field
          label="Send"
          hint={
            from === "auto"
              ? "A name like “Dad passport 2019” is often enough, and sends far less. Scanner names (CamScanner 03-15-2021…) say nothing, so those send the text."
              : from === "title"
                ? "Only names leave the server. Documents with scanner names get poor suggestions."
                : "Best suggestions, most text sent."
          }
        >
          <select
            class="input select"
            value={from}
            onChange={(e) => setFrom(e.currentTarget.value as typeof from)}
          >
            <option value="auto">The name, or the text when the name says nothing</option>
            <option value="title">Only the document's name</option>
            <option value="text">The name and the text</option>
          </select>
        </Field>
        <Field
          label="People"
          hint="Their names become [person:1], [person:2]… before anything is sent, so no names leave the server; suggestions tag documents with them. Add full names, other spellings, and what file names call them (Dad, Papa, Mummy)."
        >
          <div class="people">
            {people.map((p, i) => (
              <div key={i} class="person-row">
                <input
                  class="input"
                  placeholder="Name (the tag)"
                  aria-label="Name"
                  value={p.name}
                  onInput={(e) =>
                    setPeople(
                      people.map((x, j) => (j === i ? { ...x, name: e.currentTarget.value } : x)),
                    )
                  }
                />
                <input
                  class="input"
                  placeholder="Other names, comma-separated"
                  aria-label="Other names"
                  value={p.aliases}
                  onInput={(e) =>
                    setPeople(
                      people.map((x, j) =>
                        j === i ? { ...x, aliases: e.currentTarget.value } : x,
                      ),
                    )
                  }
                />
                <button
                  type="button"
                  class="icon-btn"
                  aria-label="Remove"
                  onClick={() => setPeople(people.filter((_, j) => j !== i))}
                >
                  <X size={14} />
                </button>
              </div>
            ))}
            <button
              type="button"
              class="btn btn-small"
              onClick={() => setPeople([...people, { name: "", aliases: "" }])}
            >
              <Plus size={13} /> Add a person
            </button>
          </div>
        </Field>
        <Field
          label="Also mask"
          hint="One per line: a surname, your street, a company. Replaced with [masked]."
        >
          <textarea
            class="input textarea"
            rows={3}
            value={words}
            onInput={(e) => setWords(e.currentTarget.value)}
          />
        </Field>
        {save.error && <div class="form-error">{save.error}</div>}
        {dirty && (
          <div class="toolbar">
            <button
              class="btn btn-primary"
              disabled={save.busy}
              onClick={() =>
                save.run(async () => {
                  const { languages: _l, classifier: _c, ...rest } = props.settings;
                  props.onSaved(
                    await api.saveSettings({
                      ...rest,
                      people: edited,
                      mask_words: splitList(words),
                      classify_from: from,
                      suggest_new: when,
                    }),
                  );
                })
              }
            >
              Save
            </button>
          </div>
        )}

        <Field
          label="Try it"
          hint="Paste text from a document to see what would be sent. Nothing leaves the server. Document names skip the “Name:”, “Address:” rule."
        >
          <textarea
            class="input textarea"
            rows={4}
            value={sample}
            placeholder="Name: … PAN … Aadhaar …"
            onInput={(e) => setSample(e.currentTarget.value)}
          />
        </Field>
        <div class="toolbar">
          <button
            class="btn btn-small"
            disabled={!sample.trim() || test.busy}
            onClick={() => test.run(async () => setMasked((await api.mask(sample)).text))}
          >
            Mask it
          </button>
          {dirty && <span class="muted">Uses the saved people and words.</span>}
        </div>
        {test.error && <div class="form-error">{test.error}</div>}
        {masked !== null && (
          <div class="doc-text open">
            <pre>{masked}</pre>
          </div>
        )}
      </div>
    </section>
  );
}
