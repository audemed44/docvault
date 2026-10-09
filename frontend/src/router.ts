import { useEffect, useState } from "preact/hooks";

/**
 * Path routes (the server answers every non-API path with the app):
 *   /                    home: search, what's expiring, categories, the latest
 *   /documents           every document, with filters
 *   /documents/:id       one document
 *   /add                 add a document: take a photo or choose files
 *   /import              bulk import (a folder from this computer, or the server's import folder)
 *   /settings            account, iPhone setup, and (admins) people, categories, OCR
 */
export type Route =
  | { page: "home" }
  | { page: "list" }
  | { page: "document"; id: number }
  | { page: "add" }
  | { page: "import" }
  | { page: "settings" };

export function parseRoute(path: string): Route {
  const parts = path.split("/").filter(Boolean);
  switch (parts[0]) {
    case "documents": {
      if (parts.length === 1) return { page: "list" };
      const id = Number(parts[1]);
      if (Number.isInteger(id) && id > 0) return { page: "document", id };
      break;
    }
    case "add":
    case "import":
    case "settings":
      return { page: parts[0] };
  }
  return { page: "home" };
}

const listeners = new Set<() => void>();

export function navigate(url: string, replace = false) {
  if (url === window.location.pathname) return;
  // Each entry knows how deep into the app it is, so Back can tell
  // whether there's a page of ours to go back to.
  const depth: number = history.state?.depth ?? 0;
  if (replace) history.replaceState({ depth }, "", url);
  else history.pushState({ depth: depth + 1 }, "", url);
  window.scrollTo(0, 0);
  listeners.forEach((fn) => fn());
}

/** Lets plain <a href="/…"> links navigate without a page load. */
export function onLinkClick(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) {
    return;
  }
  const a = (e.target as Element).closest("a");
  if (
    !a ||
    a.target ||
    a.hasAttribute("download") ||
    a.origin !== window.location.origin ||
    a.pathname.startsWith("/api/")
  ) {
    return;
  }
  e.preventDefault();
  navigate(a.pathname);
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(window.location.pathname));
  useEffect(() => {
    const update = () => setRoute(parseRoute(window.location.pathname));
    listeners.add(update);
    window.addEventListener("popstate", update);
    return () => {
      listeners.delete(update);
      window.removeEventListener("popstate", update);
    };
  }, []);
  return route;
}

/** Back to the previous page in the app, or to the fallback when there isn't one. */
export function goBack(fallback = "/") {
  if ((history.state?.depth ?? 0) > 0) history.back();
  else navigate(fallback, true);
}
