import { useState } from "preact/hooks";
import { api } from "../api";
import type { Session } from "../types";
import { Field } from "./ui";

/** Sign-in, or on a fresh install, creating the first (admin) account. */
export function Login(props: { setup: boolean; onDone: (s: Session) => void }) {
  const [token, setToken] = useState("");
  const [username, setUsername] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      props.onDone(
        props.setup
          ? await api.setup({ token, username, name, password })
          : await api.login(username, password),
      );
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="login">
      <form class="login-card" onSubmit={submit}>
        <div class="eyebrow eyebrow-accent">Docvault</div>
        <h1 class="login-title">{props.setup ? "Set up" : "Sign in"}</h1>
        {props.setup && (
          <p class="muted">
            Create the first account. It's an admin, and can add the others. Prove it's your server
            with the <code>DOCVAULT_TOKEN</code> from its settings.
          </p>
        )}
        {props.setup && (
          <Field label="DOCVAULT_TOKEN">
            <input
              class="input code"
              type="password"
              autocomplete="off"
              value={token}
              onInput={(e) => setToken(e.currentTarget.value)}
            />
          </Field>
        )}
        <Field label="Username">
          <input
            class="input"
            autocapitalize="none"
            autocorrect="off"
            autocomplete="username"
            value={username}
            autofocus={!props.setup}
            onInput={(e) => setUsername(e.currentTarget.value)}
          />
        </Field>
        {props.setup && (
          <Field label="Your name" hint="Shown on what you add">
            <input
              class="input"
              autocomplete="name"
              value={name}
              onInput={(e) => setName(e.currentTarget.value)}
            />
          </Field>
        )}
        <Field label="Password" hint={props.setup ? "At least 8 characters" : undefined}>
          <input
            class="input"
            type="password"
            autocomplete={props.setup ? "new-password" : "current-password"}
            value={password}
            onInput={(e) => setPassword(e.currentTarget.value)}
          />
        </Field>
        {error && <div class="form-error">{error}</div>}
        <button
          class="btn btn-primary btn-big"
          disabled={!username || !password || (props.setup && !token) || busy}
        >
          {props.setup ? "Create account" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
