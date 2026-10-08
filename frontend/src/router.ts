import { useEffect, useState } from "preact/hooks";

/**
 * Path routes (the server answers every non-API path with the app):
 *   /                    the library: search, filters, documents
 *   /documents/:id       one document
 *   /import              bulk import (a folder from this computer, or the server's import folder)
 *   /settings            account, iPhone setup, and (admins) people, categories, OCR
 */
export type Route =
  { page: "home" } | { page: "document"; id: number } | { page: "import" } | { page: "settings" };

export function parseRoute(path: string): Route {
  const parts = path.split("/").filter(Boolean);
  switch (parts[0]) {
    case "documents": {
      const id = Number(parts[1]);
      if (Number.isInteger(id) && id > 0) return { page: "document", id };
      break;
    }
    case "import":
    case "settings":
      return { page: parts[0] };
  }
  return { page: "home" };
}

const listeners = new Set<() => void>();

export function navigate(url: string, replace = false) {
  if (url === window.location.pathname) return;
  if (replace) history.replaceState(null, "", url);
  else history.pushState(null, "", url);
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
