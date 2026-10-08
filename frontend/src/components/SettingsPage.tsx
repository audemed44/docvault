import { ArrowDown, ArrowUp, Plus, Smartphone, Trash2, X } from "lucide-preact";
import { useState } from "preact/hooks";
import { api } from "../api";
import { useData } from "../hooks";
import { ago, langName, plural } from "../lib";
import type { APIToken, Category, Settings, User } from "../types";
import { MaskingSection, TagsSection } from "./SuggestSettings";
import { CopyField, Dialog, Empty, ErrorNote, Field, SectionHead, useAction } from "./ui";

export function SettingsPage(props: { user: User; onUser: (u: User) => void }) {
  const { user } = props;
  const settings = useData(api.settings);
  let n = 0;
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">{user.name}</div>
        <h1 class="page-title">Settings</h1>
      </header>
      <IPhoneSection index={++n} settings={settings.data} />
      <AccountSection index={++n} user={user} onUser={props.onUser} />
      {user.admin && <PeopleSection index={++n} me={user} />}
      {user.admin && <CategoriesSection index={++n} />}
      {user.admin && <TagsSection index={++n} />}
      {user.admin && settings.data && (
        <MaskingSection
          key={JSON.stringify([settings.data.people, settings.data.mask_words])}
          index={++n}
          settings={settings.data}
          onSaved={settings.setData}
        />
      )}
      {user.admin && settings.data && (
        <ProcessingSection index={++n} settings={settings.data} onSaved={settings.setData} />
      )}
    </div>
  );
}

function IPhoneSection(props: { index: number; settings: Settings | null }) {
  const tokens = useData(api.tokens);
  const [name, setName] = useState("");
  const [created, setCreated] = useState<APIToken | null>(null);
  const { busy, error, run } = useAction();
  const origin = window.location.origin;

  return (
    <section class="section">
      <SectionHead index={props.index} title="iPhone" />
      <p class="muted page-lede">
        Scan in the Files or Notes app, then <strong>Share → Save to Vault</strong>: pick a
        category, type a title, done. The Shortcut needs a personal token, which only works for your
        account and can only upload and read, not change settings.
      </p>
      <ol class="steps">
        <li>Make a token below (one per phone) and copy it.</li>
        <li>
          {props.settings?.shortcut_url ? (
            <>
              On the iPhone, open{" "}
              <a class="link-btn" href={props.settings.shortcut_url} target="_blank" rel="noopener">
                the Save to Vault Shortcut
              </a>{" "}
              and add it. It asks for the token once.
            </>
          ) : (
            <>
              Build the Shortcut: <em>Receive PDFs and Images from Share Sheet</em> →{" "}
              <em>Choose from Menu</em> with the categories → <em>Ask for Input</em> “Title?” →{" "}
              <em>Get Contents of URL</em>: POST, header{" "}
              <code>Authorization: Bearer &lt;token&gt;</code>, form fields <code>file</code>,{" "}
              <code>category</code>, <code>title</code> → show the answer as a notification.
            </>
          )}
        </li>
        <li>Tailscale needs to be on. The Shortcut shows “ok: saved …” or what went wrong.</li>
      </ol>
      <div class="kv kv-wide">
        <div>
          <dt>Upload to</dt>
          <dd>
            <CopyField value={`${origin}/api/upload`} />
          </dd>
        </div>
        <div>
          <dt>Category list</dt>
          <dd>
            <CopyField value={`${origin}/api/categories?format=names`} />
          </dd>
        </div>
      </div>

      {created?.token && (
        <div class="note note-accent">
          <div class="eyebrow eyebrow-accent">Token for {created.name}</div>
          <p>Copy it now: it isn't shown again.</p>
          <CopyField value={created.token} label="Copy the token" />
        </div>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      {tokens.data && tokens.data.length > 0 && (
        <div class="list">
          {tokens.data.map((t) => (
            <div key={t.id} class="list-row">
              <Smartphone size={16} class="muted" />
              <span class="list-main list-link">
                <span class="list-title">{t.name}</span>
                <span class="list-sub mono">
                  …{t.hint} · made {ago(t.created)}
                  {t.used ? ` · used ${ago(t.used)}` : " · not used yet"}
                </span>
              </span>
              <button
                class="icon-btn"
                aria-label={`Revoke ${t.name}`}
                title="Revoke"
                disabled={busy}
                onClick={() =>
                  run(async () => {
                    if (!confirm(`Revoke the token “${t.name}”? Its Shortcut stops working.`)) {
                      return;
                    }
                    await api.deleteToken(t.id);
                    if (created?.id === t.id) setCreated(null);
                    await tokens.reload();
                  })
                }
              >
                <Trash2 size={15} />
              </button>
            </div>
          ))}
        </div>
      )}
      <form
        class="toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          run(async () => {
            setCreated(await api.createToken(name.trim()));
            setName("");
            await tokens.reload();
          });
        }}
      >
        <input
          class="input input-inline"
          placeholder="Name, like “Dad's iPhone”"
          value={name}
          onInput={(e) => setName(e.currentTarget.value)}
        />
        <button class="btn btn-primary" disabled={!name.trim() || busy}>
          <Plus size={14} /> Make a token
        </button>
      </form>
    </section>
  );
}

function AccountSection(props: { index: number; user: User; onUser: (u: User) => void }) {
  const [name, setName] = useState(props.user.name);
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [saved, setSaved] = useState("");
  const { busy, error, run } = useAction();

  const save = (e: Event) => {
    e.preventDefault();
    setSaved("");
    run(async () => {
      const res = await api.updateMe({
        name,
        current_password: current || undefined,
        password: password || undefined,
      });
      props.onUser("user" in res && res.user ? res.user : (res as User));
      setCurrent("");
      setPassword("");
      setSaved(password ? "Saved. Your other devices are signed out." : "Saved.");
    });
  };

  return (
    <section class="section">
      <SectionHead index={props.index} title="Account">
        <span class="muted mono">{props.user.username}</span>
      </SectionHead>
      <form class="form form-narrow" onSubmit={save}>
        <Field label="Name">
          <input class="input" value={name} onInput={(e) => setName(e.currentTarget.value)} />
        </Field>
        <div class="form-grid">
          <Field label="Current password">
            <input
              class="input"
              type="password"
              autocomplete="current-password"
              value={current}
              onInput={(e) => setCurrent(e.currentTarget.value)}
            />
          </Field>
          <Field label="New password" hint="Blank keeps it">
            <input
              class="input"
              type="password"
              autocomplete="new-password"
              value={password}
              onInput={(e) => setPassword(e.currentTarget.value)}
            />
          </Field>
        </div>
        {error && <div class="form-error">{error}</div>}
        {saved && <div class="tone-good">{saved}</div>}
        <div class="toolbar">
          <button
            class="btn btn-primary"
            disabled={busy || !name.trim() || (!!password && !current)}
          >
            Save
          </button>
        </div>
      </form>
    </section>
  );
}

function PeopleSection(props: { index: number; me: User }) {
  const users = useData(api.users);
  const [editing, setEditing] = useState<Partial<User> | null>(null);
  return (
    <section class="section">
      <SectionHead index={props.index} title="People">
        <button
          class="btn btn-primary btn-small"
          onClick={() => setEditing({ username: "", name: "", admin: false })}
        >
          <Plus size={14} /> Add
        </button>
      </SectionHead>
      <p class="muted page-lede">
        Everyone has a private library. The Family space is shared by all of them. Admins also
        manage people, categories and OCR, but can't see others' private documents.
      </p>
      {users.error && <ErrorNote>{users.error}</ErrorNote>}
      {users.data && (
        <div class="list">
          {users.data.map((u) => (
            <div key={u.id} class="list-row">
              <span class={`dot ${u.admin ? "accent" : ""}`} />
              <button class="list-main list-link" onClick={() => setEditing(u)}>
                <span class="list-title">
                  {u.name} {u.id === props.me.id && <span class="muted">(you)</span>}
                </span>
                <span class="list-sub">
                  {u.username} · {u.admin ? "admin" : "member"} ·{" "}
                  {plural(u.documents, "private document")}
                </span>
              </button>
            </div>
          ))}
        </div>
      )}
      {editing && (
        <UserDialog
          user={editing}
          me={props.me}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            users.reload();
          }}
        />
      )}
    </section>
  );
}

function UserDialog(props: {
  user: Partial<User>;
  me: User;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [u, setU] = useState(props.user);
  const [password, setPassword] = useState("");
  const { busy, error, run } = useAction();
  const isMe = u.id === props.me.id;
  const save = (e: Event) => {
    e.preventDefault();
    run(async () => {
      await api.saveUser({ ...u, password: password || undefined });
      props.onSaved();
    });
  };
  const remove = () =>
    run(async () => {
      if (!u.id || !confirm(`Delete ${u.name}'s account?`)) return;
      await api.deleteUser(u.id);
      props.onSaved();
    });
  return (
    <Dialog
      title={u.id ? u.name || "Person" : "Add a person"}
      onClose={props.onClose}
      footer={
        <>
          {u.id && !isMe && (
            <button class="btn btn-danger" onClick={remove} disabled={busy}>
              <Trash2 size={14} /> Delete
            </button>
          )}
          <span class="spacer" />
          <button class="btn btn-ghost" onClick={props.onClose}>
            Cancel
          </button>
          <button
            class="btn btn-primary"
            form="user-form"
            disabled={busy || !u.username?.trim() || (!u.id && password.length < 8)}
          >
            Save
          </button>
        </>
      }
    >
      <form id="user-form" class="form" onSubmit={save}>
        <Field label="Username" hint={u.id ? "Can't be changed" : "What they sign in with"}>
          <input
            class="input"
            autocapitalize="none"
            value={u.username}
            disabled={!!u.id}
            onInput={(e) => setU({ ...u, username: e.currentTarget.value })}
          />
        </Field>
        <Field label="Name">
          <input
            class="input"
            value={u.name}
            onInput={(e) => setU({ ...u, name: e.currentTarget.value })}
          />
        </Field>
        <Field
          label={u.id ? "New password" : "Password"}
          hint={u.id ? "Blank keeps it; a new one signs them out" : "At least 8 characters"}
        >
          <input
            class="input"
            type="password"
            autocomplete="new-password"
            value={password}
            onInput={(e) => setPassword(e.currentTarget.value)}
          />
        </Field>
        <label class="check">
          <input
            type="checkbox"
            checked={!!u.admin}
            disabled={isMe}
            onChange={(e) => setU({ ...u, admin: e.currentTarget.checked })}
          />
          Admin
        </label>
        {error && <div class="form-error">{error}</div>}
      </form>
    </Dialog>
  );
}

function CategoriesSection(props: { index: number }) {
  const cats = useData(api.categories);
  const [list, setList] = useState<Partial<Category>[] | null>(null);
  const { busy, error, run } = useAction();
  const editing = list ?? cats.data ?? [];
  const set = (next: Partial<Category>[]) => setList(next);
  const move = (i: number, by: number) => {
    const next = [...editing];
    [next[i], next[i + by]] = [next[i + by], next[i]];
    set(next);
  };

  return (
    <section class="section">
      <SectionHead index={props.index} title="Categories" />
      <p class="muted page-lede">
        One per document. The iPhone Shortcut reads this list for its menu. Deleting a category
        moves its documents to the Inbox.
      </p>
      {cats.error && <ErrorNote>{cats.error}</ErrorNote>}
      {cats.data && (
        <div class="cat-list">
          {editing.map((c, i) => (
            <div key={c.id ?? `new-${i}`} class="cat-row">
              <input
                class="input"
                value={c.name}
                aria-label="Category name"
                onInput={(e) =>
                  set(editing.map((x, j) => (j === i ? { ...x, name: e.currentTarget.value } : x)))
                }
              />
              <span class="muted mono cat-count">{c.count ?? 0}</span>
              <button
                class="icon-btn"
                aria-label="Move up"
                disabled={i === 0}
                onClick={() => move(i, -1)}
              >
                <ArrowUp size={14} />
              </button>
              <button
                class="icon-btn"
                aria-label="Move down"
                disabled={i === editing.length - 1}
                onClick={() => move(i, 1)}
              >
                <ArrowDown size={14} />
              </button>
              <button
                class="icon-btn"
                aria-label={`Delete ${c.name}`}
                onClick={() => set(editing.filter((_, j) => j !== i))}
              >
                <X size={14} />
              </button>
            </div>
          ))}
        </div>
      )}
      {error && <ErrorNote>{error}</ErrorNote>}
      <div class="toolbar">
        <button class="btn btn-small" onClick={() => set([...editing, { name: "" }])}>
          <Plus size={13} /> Add a category
        </button>
        {list && (
          <>
            <button
              class="btn btn-primary btn-small"
              disabled={busy}
              onClick={() =>
                run(async () => {
                  const removed = (cats.data ?? []).filter(
                    (c) => c.count > 0 && !list.some((x) => x.id === c.id),
                  );
                  if (
                    removed.length &&
                    !confirm(
                      `${removed.map((c) => c.name).join(", ")} still have documents; they'll go to the Inbox. Save?`,
                    )
                  ) {
                    return;
                  }
                  cats.setData(await api.saveCategories(list));
                  setList(null);
                })
              }
            >
              Save categories
            </button>
            <button class="btn btn-ghost btn-small" onClick={() => setList(null)}>
              Undo
            </button>
          </>
        )}
      </div>
    </section>
  );
}

function ProcessingSection(props: {
  index: number;
  settings: Settings;
  onSaved: (s: Settings) => void;
}) {
  const [s, setS] = useState(props.settings);
  const { busy, error, run } = useAction();
  const langs = s.languages.length ? s.languages : ["eng", "hin"];
  const options = Array.from(new Set([...langs, langs.join("+"), s.ocr_langs]));
  const dirty =
    s.ocr_langs !== props.settings.ocr_langs || s.shortcut_url !== props.settings.shortcut_url;
  return (
    <section class="section">
      <SectionHead index={props.index} title="Processing" />
      <form
        class="form form-narrow"
        onSubmit={(e) => {
          e.preventDefault();
          run(async () =>
            props.onSaved(
              await api.saveSettings({
                ...props.settings,
                ocr_langs: s.ocr_langs,
                shortcut_url: s.shortcut_url,
              }),
            ),
          );
        }}
      >
        <Field
          label="OCR language"
          hint="For scans without their own text. Each document can be run again in another."
        >
          <select
            class="input select"
            value={s.ocr_langs}
            onChange={(e) => setS({ ...s, ocr_langs: e.currentTarget.value })}
          >
            {options.map((l) => (
              <option key={l} value={l}>
                {langName(l)}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Shortcut link" hint="The iCloud link to share the Save to Vault Shortcut">
          <input
            class="input"
            type="url"
            placeholder="https://www.icloud.com/shortcuts/…"
            value={s.shortcut_url}
            onInput={(e) => setS({ ...s, shortcut_url: e.currentTarget.value })}
          />
        </Field>
        {error && <div class="form-error">{error}</div>}
        {dirty && (
          <div class="toolbar">
            <button class="btn btn-primary" disabled={busy}>
              Save
            </button>
          </div>
        )}
      </form>
      {!s.languages.length && (
        <Empty>Tesseract isn't installed here, so OCR won't run (it is in the Docker image).</Empty>
      )}
    </section>
  );
}
