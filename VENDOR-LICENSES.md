# Vendored third-party licences

Every file in `static/vendor/` is third-party code committed into this tree.
Each one is declared in the manifest of the module that owns it, with its
source URL, version, byte count, SHA-256 and licence; `ggg registry build`
verifies the bytes on disk against that declaration and refuses to build on
drift.

Nothing here is fetched at runtime from a CDN. The lazily loaded engines
(Chart.js, Cally, SortableJS) are served from this origin with subresource
integrity.

| Package | Licence | Owning module |
| --- | --- | --- |
| `@alpinejs/csp@3.15.12` | MIT | `system/static` |
| `@alpinejs/focus@3.15.12` | MIT | `system/static` |
| `@clerk/clerk-js@5.127.1` | MIT | `system/identity` |
| `cally@0.9.2` | MIT | `component/calendar` |
| `chart.js@4.5.1` | MIT | `component/chart` |
| `htmx.org@4.0.0-beta6` | BSD-2-Clause | `system/static` |
| `sortablejs@1.15.7/Sortable.min.js` | MIT | `component/kanban` |

## Notices retained inside the files themselves

Chart.js embeds its own MIT notice and that of the bundled `@kurkle/color`;
Cally embeds its MIT notice and that of the bundled Atomico. Those licences
require the notice to travel with the code, and it does - inside the committed
bytes, which is why there is no separate copy here to drift out of date.
