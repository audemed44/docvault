import { describe, expect, it } from "vitest";
import { filterQuery } from "./api";
import {
  ago,
  categoryColor,
  INBOX_COLOR,
  daysUntil,
  expiryText,
  formatDate,
  langName,
  plural,
  snippetParts,
} from "./lib";
import { parseRoute } from "./router";

describe("format", () => {
  const now = Date.parse("2026-10-02T12:00:00Z");
  it("ago", () => {
    expect(ago("2026-10-02T11:59:30Z", now)).toBe("just now");
    expect(ago("2026-10-02T09:00:00Z", now)).toBe("3h ago");
    expect(ago("2026-09-28T12:00:00Z", now)).toBe("4d ago");
  });
  it("plural", () => {
    expect(plural(1, "page")).toBe("1 page");
    expect(plural(3, "page")).toBe("3 pages");
  });
  it("dates", () => {
    expect(formatDate("2024-03-05")).toBe("5 Mar 2024");
    const day = new Date(2026, 9, 2);
    expect(daysUntil("2026-10-12", day)).toBe(10);
    expect(expiryText("2026-10-01", day)).toEqual({ text: "Expired 1 Oct 2026", tone: "bad" });
    expect(expiryText("2026-10-12", day).tone).toBe("warn");
    expect(expiryText("2028-01-01", day).tone).toBe("");
  });
  it("snippets", () => {
    expect(snippetParts("a \u0002pass\u0003port\n no")).toEqual([
      { text: "a ", match: false },
      { text: "pass", match: true },
      { text: "port no", match: false },
    ]);
  });
  it("languages", () => {
    expect(langName("eng+hin")).toBe("English + Hindi");
  });
});

describe("api", () => {
  it("builds filter queries", () => {
    expect(filterQuery({ q: " pan card ", category: "none", expiring: true })).toBe(
      "?q=pan+card&category=none&expiring=1",
    );
    expect(filterQuery({})).toBe("");
  });
});

describe("router", () => {
  it("parses routes", () => {
    expect(parseRoute("/")).toEqual({ page: "home" });
    expect(parseRoute("/documents/12")).toEqual({ page: "document", id: 12 });
    expect(parseRoute("/documents/x")).toEqual({ page: "home" });
    expect(parseRoute("/import")).toEqual({ page: "import" });
    expect(parseRoute("/settings")).toEqual({ page: "settings" });
    expect(parseRoute("/nope")).toEqual({ page: "home" });
  });
});

describe("categoryColor", () => {
  it("gives the usual categories their own colours", () => {
    const names = [
      "ID",
      "Property",
      "Medical",
      "Insurance",
      "Tax",
      "Bills",
      "Vehicle",
      "Education",
      "Banking & Investments",
      "Travel",
      "Work",
      "Other",
    ];
    expect(new Set(names.map(categoryColor)).size).toBe(names.length);
  });
  it("keeps the inbox grey and picks a steady colour for others", () => {
    expect(categoryColor("")).toBe(INBOX_COLOR);
    expect(categoryColor("Pets")).toBe(categoryColor("pets"));
    expect(categoryColor("Pets")).toMatch(/^#[0-9a-f]{6}$/);
  });
});
