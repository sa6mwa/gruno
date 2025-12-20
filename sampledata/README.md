Sample Bruno collection for gru tests (multi-domain but generic).

Folders & cases (seq order):
- basic-request (seq 0)
- Users: create-user (1), list-users (2), get-user (2.1), update-user (3), delete-user (4)
- Shipping: gate-in (3), load-container (4), gate-out (5), unload-container (6), status (7)
- Finance: post-invoice (6), pay-invoice (7), get-invoice (8), list-invoices (9)
- Assertions: not-have-property (10), regex-match (10.1)
- Trace: trace-id-propagation (11), trace-id-generation (11.1)

Environment: environments/local.bru (baseUrl placeholder, apiKey, and static ids).
Prelude: environments/prelude.js (used by Shipping/status).
Bruno manifest: bruno.json at root.
