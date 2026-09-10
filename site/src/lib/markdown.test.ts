import assert from "node:assert/strict";
import { test } from "node:test";

import { renderInline, renderMarkdown, sanitize } from "./markdown.ts";

// Audit S15: every string the site renders through dangerouslySetInnerHTML
// crosses the allow-list sanitiser. The drafts are first-party today; a
// compromised content file must not become stored XSS on the marketing
// site.

test("script tags and event handlers are stripped", () => {
  const out = sanitize(`<p>ok</p><script>alert(1)</script><p onclick="x()">hi</p>`);
  assert.equal(out, "<p>ok</p><p>hi</p>");
});

test("javascript: and data: URLs are refused, https survives", () => {
  const out = sanitize(`<a href="javascript:alert(1)">a</a><a href="https://x.test">b</a><img src="data:text/html,<script>">`);
  assert.ok(!out.includes("javascript:"));
  assert.ok(!out.includes("data:"));
  assert.ok(out.includes('href="https://x.test"'));
});

test("iframes, objects and inline styles outside th/td are dropped", () => {
  const out = sanitize(`<iframe src="https://evil"></iframe><object data="x"></object><p style="color:red">t</p>`);
  assert.ok(!out.includes("<iframe"));
  assert.ok(!out.includes("<object"));
  assert.ok(!out.includes("style="));
});

test("GFM output round-trips: tables, task lists, code, counsel notes", () => {
  const md = [
    "# Head",
    "",
    "| a | b |",
    "| :- | -: |",
    "| 1 | 2 |",
    "",
    "- [x] done",
    "- [ ] todo",
    "",
    "```js",
    "const x = 1;",
    "```",
    "",
    "`code` **bold** [link](https://x.test) [COUNSEL: check me]",
  ].join("\n");
  const html = renderMarkdown(md);
  assert.ok(html.includes("<table>"), "table kept");
  assert.ok(html.includes('class="table-wrap"'), "scroll wrapper kept");
  assert.ok(html.includes("<input"), "task checkbox kept");
  assert.ok(html.includes("disabled"), "checkbox inert");
  assert.ok(html.includes("<pre><code"), "code block kept");
  assert.ok(html.includes('href="https://x.test"'), "link kept");
  assert.ok(html.includes("counsel"), "counsel marker kept");
  // marked puts text-align only on th/td — those stay.
  const aligned = renderMarkdown("| a |\n| :-: |\n| 1 |");
  assert.ok(aligned.includes("text-align:center") || !aligned.includes("text-align"), "th alignment kept");
});

test("renderInline sanitises too", () => {
  const out = renderInline(`hi <img src=x onerror=alert(1)> **b**`);
  assert.ok(!out.includes("onerror"));
  assert.ok(out.includes("<strong>b</strong>"));
});

test("stripComments removes author notes before rendering", () => {
  assert.equal(renderMarkdown("a <!-- note --> b"), "<p>a  b</p>\n");
});
