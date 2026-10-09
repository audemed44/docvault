import {
  ArrowLeft,
  ChevronDown,
  Files,
  LogOut,
  Plus,
  Settings as SettingsIcon,
} from "lucide-preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { api, setUnauthorizedHandler } from "./api";
import { AddPage } from "./components/AddPage";
import { DocumentPage } from "./components/DocumentPage";
import { ImportPage } from "./components/ImportPage";
import { LibraryPage } from "./components/LibraryPage";
import { Login } from "./components/Login";
import { SettingsPage } from "./components/SettingsPage";
import { onLinkClick, useRoute, type Route } from "./router";
import type { Session, User } from "./types";

export function App() {
  const route = useRoute();
  const [session, setSession] = useState<Session | null>(null);
  const [error, setError] = useState("");

  const load = () =>
    api
      .session()
      .then(setSession)
      .catch((e: Error) => setError(e.message));

  useEffect(() => {
    setUnauthorizedHandler(() => load());
    load();
  }, []);

  // Each person's text size scales every rem in the stylesheet.
  const textSize = session?.user?.text_size ?? "";
  useEffect(() => {
    if (textSize) document.documentElement.dataset.textSize = textSize;
    else delete document.documentElement.dataset.textSize;
  }, [textSize]);

  if (error) return <div class="boot">Can't reach Docvault: {error}</div>;
  if (!session) return <div class="boot" />;
  if (!session.authenticated || !session.user) {
    return <Login setup={!!session.setup_needed} onDone={setSession} />;
  }
  return (
    <Shell
      user={session.user}
      foyerURL={session.foyer_url}
      route={route}
      onUser={(user) => setSession({ ...session, user })}
      onSignOut={() => setSession({ authenticated: false })}
    />
  );
}

const NAV: { page: Route["page"]; href: string; label: string; admin?: boolean }[] = [
  { page: "home", href: "/", label: "Documents" },
  { page: "add", href: "/add", label: "Add" },
  { page: "import", href: "/import", label: "Import", admin: true },
  { page: "settings", href: "/settings", label: "Settings" },
];

function Shell(props: {
  route: Route;
  user: User;
  foyerURL?: string;
  onUser: (u: User) => void;
  onSignOut: () => void;
}) {
  const { route, user } = props;
  const signOut = async () => {
    await api.logout().catch(() => {});
    props.onSignOut();
  };
  const active = route.page === "document" || route.page === "list" ? "home" : route.page;
  return (
    <div class="shell" onClick={onLinkClick}>
      <header class="topbar">
        {props.foyerURL && (
          <a class="home-link" href={props.foyerURL} title="Back to Foyer">
            <ArrowLeft size={14} />
            <span class="home-link-text">Foyer</span>
          </a>
        )}
        <a class="brand" href="/">
          <span class="brand-mark" aria-hidden="true" />
          <span class="brand-text">Docvault</span>
        </a>
        <span class="spacer" />
        <nav class="topnav" aria-label="Pages">
          {NAV.filter((n) => !n.admin || user.admin).map((n) => (
            <a key={n.page} class={active === n.page ? "active" : ""} href={n.href}>
              {n.label}
            </a>
          ))}
        </nav>
        <AccountMenu user={user} onSignOut={signOut} />
      </header>
      <main>
        {(route.page === "home" || route.page === "list") && (
          <LibraryPage user={user} browse={route.page === "list"} />
        )}
        {route.page === "document" && <DocumentPage key={route.id} id={route.id} user={user} />}
        {route.page === "add" && <AddPage user={user} />}
        {route.page === "import" && <ImportPage user={user} />}
        {route.page === "settings" && <SettingsPage user={user} onUser={props.onUser} />}
      </main>
      <nav class="tabbar" aria-label="Pages">
        <a class={active === "home" ? "active" : ""} href="/">
          <span class="tab-icon">
            <Files size={24} />
          </span>
          Documents
        </a>
        <a class={`tab-add ${active === "add" ? "active" : ""}`} href="/add">
          <span class="tab-icon tab-add-icon">
            <Plus size={26} />
          </span>
          Add
        </a>
        <a class={active === "settings" ? "active" : ""} href="/settings">
          <span class="tab-icon">
            <SettingsIcon size={24} />
          </span>
          Settings
        </a>
      </nav>
    </div>
  );
}

/** The person's name; opens to text size and Sign out. */
function AccountMenu(props: { user: User; onSignOut: () => void }) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (e: Event) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);
  return (
    <div class="account" ref={root}>
      <button
        class="account-btn"
        aria-expanded={open}
        aria-haspopup="menu"
        onClick={() => setOpen(!open)}
      >
        <span class="account-name">{props.user.name}</span>
        <ChevronDown size={16} />
      </button>
      {open && (
        <div class="account-menu" role="menu">
          <div class="account-who">
            Signed in as <strong>{props.user.name}</strong>
          </div>
          <a role="menuitem" href="/settings" onClick={() => setOpen(false)}>
            <SettingsIcon size={18} /> Text size and settings
          </a>
          <button role="menuitem" onClick={props.onSignOut}>
            <LogOut size={18} /> Sign out
          </button>
        </div>
      )}
    </div>
  );
}
