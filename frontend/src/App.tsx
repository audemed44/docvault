import { ArrowLeft, LogOut } from "lucide-preact";
import { useEffect, useState } from "preact/hooks";
import { api, setUnauthorizedHandler } from "./api";
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

const NAV: { page: Route["page"]; href: string; label: string }[] = [
  { page: "home", href: "/", label: "Library" },
  { page: "import", href: "/import", label: "Import" },
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
  const active = route.page === "document" ? "home" : route.page;
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
          {NAV.map((n) => (
            <a key={n.page} class={active === n.page ? "active" : ""} href={n.href}>
              {n.label}
            </a>
          ))}
        </nav>
        <button
          class="icon-btn"
          onClick={signOut}
          title={`Sign out ${user.name}`}
          aria-label="Sign out"
        >
          <LogOut size={16} />
        </button>
      </header>
      <main>
        {route.page === "home" && <LibraryPage user={user} />}
        {route.page === "document" && <DocumentPage key={route.id} id={route.id} user={user} />}
        {route.page === "import" && <ImportPage user={user} />}
        {route.page === "settings" && <SettingsPage user={user} onUser={props.onUser} />}
      </main>
    </div>
  );
}
