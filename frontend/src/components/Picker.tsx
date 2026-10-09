import { Check, Search } from "lucide-preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";

export interface PickOption {
  value: string;
  label: string;
  count?: number;
}

/** Rows a menu shows before it scrolls; a longer list gets a search box. */
export const MENU_ROWS = 8;

/**
 * A filter dropdown: like a <select>, but a long list (more than MENU_ROWS)
 * opens with a search box, since a phone's picker wheel can't be searched.
 */
export function Picker(props: {
  value: string;
  options: PickOption[];
  onChange: (v: string) => void;
  label: string;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [alignRight, setAlignRight] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const searchable = props.options.length > MENU_ROWS;
  const current = props.options.find((o) => o.value === props.value) ?? props.options[0];

  const q = query.trim().toLowerCase();
  const shown = q
    ? props.options.filter((o) => o.value !== "" && o.label.toLowerCase().includes(q))
    : props.options;

  const close = () => {
    setOpen(false);
    setQuery("");
  };
  const pick = (o: PickOption) => {
    close();
    if (o.value !== props.value) props.onChange(o.value);
  };

  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) close();
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);

  // Opens toward the side with room, so a picker on the right of a phone
  // screen doesn't run off it.
  useLayoutEffect(() => {
    if (!open || !menu.current || !root.current) return;
    const left = root.current.getBoundingClientRect().left;
    setAlignRight(left + menu.current.offsetWidth > window.innerWidth - 8);
    const i = shown.findIndex((o) => o.value === props.value);
    setActive(Math.max(0, i));
    menu.current.querySelector(".picker-option.selected")?.scrollIntoView({ block: "nearest" });
    menu.current.querySelector<HTMLInputElement>(".picker-search input")?.focus();
  }, [open]);

  useEffect(() => setActive(0), [query]);

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      close();
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const n = shown.length;
      if (n) setActive((a) => (a + (e.key === "ArrowDown" ? 1 : n - 1)) % n);
    } else if (e.key === "Enter" && shown[active]) {
      e.preventDefault();
      pick(shown[active]);
    }
  };

  return (
    <div class="picker" ref={root} onKeyDown={open ? onKey : undefined}>
      <button
        type="button"
        class={`input select picker-button ${props.value ? "picker-set" : ""}`}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={props.label}
        onClick={() => (open ? close() : setOpen(true))}
      >
        <span class="picker-value">
          {current?.label}
          {current?.count !== undefined && <span class="picker-count"> ({current.count})</span>}
        </span>
      </button>
      {open && (
        <div class={`picker-menu ${alignRight ? "picker-menu-right" : ""}`} ref={menu}>
          {searchable && (
            <label class="picker-search">
              <Search size={14} />
              <input
                type="search"
                placeholder={`Search ${props.label.toLowerCase()}`}
                value={query}
                enterkeyhint="go"
                onInput={(e) => setQuery(e.currentTarget.value)}
              />
            </label>
          )}
          <div class="picker-list" role="listbox" aria-label={props.label}>
            {shown.map((o, i) => (
              <button
                type="button"
                role="option"
                key={o.value}
                aria-selected={o.value === props.value}
                class={`picker-option ${o.value === props.value ? "selected" : ""} ${i === active ? "active" : ""}`}
                onMouseEnter={() => setActive(i)}
                onClick={() => pick(o)}
              >
                <span class="picker-label">{o.label}</span>
                {o.count !== undefined && <span class="picker-count">{o.count}</span>}
                {o.value === props.value && <Check size={13} class="picker-check" />}
              </button>
            ))}
            {!shown.length && <div class="picker-empty">Nothing matches.</div>}
          </div>
        </div>
      )}
    </div>
  );
}
