import { useState } from "preact/hooks";
import { api } from "../api";
import type { Session } from "../types";
import { Field } from "./ui";

/** Sign-in with a username, or on a fresh install, making the first (admin) account. */
export function Login(props: { setup: boolean; onDone: (s: Session) => void }) {
  const [username, setUsername] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      props.onDone(props.setup ? await api.setup({ username, name }) : await api.login(username));
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
          <p class="muted">Make the first account. It's an admin, and adds everyone else.</p>
        )}
        <Field label="Username">
          <input
            class="input"
            autocapitalize="none"
            autocorrect="off"
            autocomplete="username"
            value={username}
            autofocus
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
        {error && <div class="form-error">{error}</div>}
        <button class="btn btn-primary btn-big" disabled={!username.trim() || busy}>
          {props.setup ? "Create account" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
