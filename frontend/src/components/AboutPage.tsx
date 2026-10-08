import { SectionHead } from "./ui";

export function AboutPage() {
  return (
    <div class="page">
      <header class="page-head">
        <div class="eyebrow eyebrow-accent">About</div>
        <h1 class="page-title">Docvault</h1>
        <p class="muted page-lede">Self-hosted home for scanned family documents: iPhone Shortcut uploads, OCR search, private and family libraries.</p>
      </header>
      <section class="section">
        <SectionHead index={1} title="Foyer" />
        <p class="muted page-lede">
          Add Docvault to Foyer as an <code>app</code> widget at{" "}
          <code>http://docvault:8080/api/foyer/widget</code>, with the token as its key.
        </p>
      </section>
    </div>
  );
}
