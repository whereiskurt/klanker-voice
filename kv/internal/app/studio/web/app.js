/* kv studio — operator console binding.
 *
 * Vanilla JS, no framework, no module imports. Network calls are all
 * same-origin loopback REST: `GET /api/config` (read), plus the Plan-04
 * write surface this file drives — POST/PUT/DELETE /api/rules, PUT
 * /api/order, PUT /api/secret, GET/POST/PUT /api/dids (see
 * kv/internal/app/studio/types.go for the pinned JSON contracts, and
 * server.go for the exact handler behavior). On load this fetches the live,
 * assembled ConfigView and renders it across the Rules, DIDs, Knowledge and
 * Keys surfaces. A non-2xx response, or a payload carrying a non-null
 * `error`, reveals the top-of-page error banner naming the region + profile
 * and never leaves a blank screen.
 *
 * Every payload-derived string is inserted via textContent/createElement —
 * never raw innerHTML — so a store-sourced string cannot inject markup. The
 * browser never writes a store directly: every mutation goes through one of
 * the REST endpoints above, which is the sole write authority (spec §7).
 *
 * Phase 17 additions (KNOW-02/KNOW-03/SEC-01/SEC-02):
 *   - Knowledge tab: POST /api/manifest/sources (add-source form per pack)
 *     and POST /api/knowledge/rebuild (rebuild button + report panel).
 *   - Keys & secrets tab: POST /api/secret/reveal and POST /api/secret/rotate
 *     for the three allow-listed telephony gate secrets. A revealed value is
 *     written ONLY into a transient DOM node's textContent (see renderKeys'
 *     reveal handler) — never localStorage, sessionStorage, a cookie, or any
 *     cache. Hiding it, or any fresh render, clears that node. The three
 *     SOPS-managed provider keys (elevenlabs/deepgram/anthropic) are a
 *     static, client-side-only display list (PROVIDER_KEYS) — ConfigView
 *     never carries them, and this file offers no reveal/rotate action for
 *     them at all (17-CONTEXT.md locked decision).
 *
 * Truthfulness notes baked into this file (RULE-05, 16-RESEARCH.md):
 *   - SECRET (gate_mode / require_gate) is ONE shared config for every rule
 *     in v1 (see SecretSpec's doc comment in types.go) — editing it always
 *     writes PUT /api/secret, never a per-rule field, and the UI says so.
 *   - An existing rule's WHO (phone) has no edit endpoint in this build —
 *     only GRANT (tierId, via PUT /api/rules/{code}) and BLOCK/DELETE are
 *     writable for an existing rule. The drawer shows WHO read-only for an
 *     existing rule rather than implying a write that doesn't exist.
 *   - GRANT's session/period/concurrency minutes are read-only display
 *     (joined from the Tier row server-side) — there is no endpoint in this
 *     build to write Tier limits directly; only the Tier ID a code points at
 *     is writable.
 *   - KNOWLEDGE and PERSONA are read-only (v1: every rule reaches every pack
 *     and persona is a single fixed value) — no write control is offered.
 *
 * Phase 18 addition (SOP-01/02/03, "Save & deploy" tab):
 *   - POST /api/sop/changeset {name} fetches the pending changeset (a named
 *     SOP file vs the current live config, per-surface added/changed/
 *     removed) and renders it; POST /api/sop/save {name} snapshots live into
 *     a new SOP and reports the resulting local commit; POST /api/sop/deploy
 *     {name} re-reads that SOP, validates it, and — only if valid — applies
 *     it to the live stores, reporting the four ordered steps' outcome. As
 *     with every other tab, this file only fetches and renders those three
 *     responses; it holds no store/version-control logic of its own. Deploy
 *     is gated in the UI: the button stays disabled until a changeset has
 *     been loaded for the exact name currently in the input, so an operator
 *     always reviews the diff immediately before deploying.
 */
(function () {
  "use strict";

  var TAB_META = {
    rules: {
      t: "Routing rules",
      d: "First match wins, top to bottom. Each rule maps a caller + secret to time, knowledge and a persona."
    },
    knowledge: {
      t: "Knowledge",
      d: "The pack library every rule can draw from — add a source, then rebuild to regenerate the pack."
    },
    keys: {
      t: "Keys & secrets",
      d: "Reveal is ephemeral and never stored client-side. Provider keys are SOPS-managed and display-only."
    },
    deploy: {
      t: "Save & deploy",
      d: "Snapshot the live config as an SOP, review the changeset, then deploy it — the only writable path off this tab."
    }
  };

  var state = {
    config: null,
    drawerRuleId: null,
    dids: [],
    didsStatus: "",
    didFormMode: null,
    didFormTarget: null,
    // sop tracks the Save & deploy tab (SOP-01/02/03): changeset/
    // changesetName gate the Deploy button — changeset stays non-null ONLY
    // while it matches changesetName === the current name-input value
    // exactly (see updateDeployGate); any change to the name input, or a
    // just-completed deploy, clears both so the operator must reload the
    // changeset before deploying again.
    sop: { changeset: null, changesetName: null }
  };

  // BLOCK_TIER_ID mirrors server.go's unexported blockTierID constant
  // ("no-access") — RULE-04's block is expressed entirely as an
  // AccessCode pointed at this zero-limit tier, never a separate flag, so
  // the console must derive "is this rule blocked" the same way the server
  // does: grant.tierId === BLOCK_TIER_ID.
  var BLOCK_TIER_ID = "no-access";

  // MANIFEST_SOURCE_KINDS mirrors repofile_writer.go's
  // allowedManifestSourceKinds — the exact four kinds WriteManifestSource
  // accepts. Kept as a client-side constant purely for the <select> options;
  // the server re-validates on every write (never trust the client).
  var MANIFEST_SOURCE_KINDS = ["docs", "code", "transcript", "diagram"];

  // PROVIDER_KEYS is a static, client-side-only display list — these three
  // pipeline API keys are SOPS-managed (not SSM), and ConfigView.Secrets
  // never carries them (secret_adapter.go's ReadSecretRefs only returns the
  // three telephony gate params). Phase 17 deliberately ships NO reveal/
  // rotate path for them: rotating a SOPS-encrypted value means editing the
  // encrypted file + re-encrypt, which is out of scope for this console in
  // v1 (17-CONTEXT.md locked decision) — so these render as inert,
  // display-only rows.
  var PROVIDER_KEYS = [
    { name: "elevenlabs", note: "TTS streaming API key" },
    { name: "deepgram", note: "STT streaming API key" },
    { name: "anthropic", note: "LLM API key" }
  ];

  function $(sel) {
    return document.querySelector(sel);
  }

  function $all(sel) {
    return Array.prototype.slice.call(document.querySelectorAll(sel));
  }

  function el(tag, className) {
    var n = document.createElement(tag);
    if (className) n.className = className;
    return n;
  }

  function text(tag, className, str) {
    var n = el(tag, className);
    n.textContent = str;
    return n;
  }

  function lampEl(kind, label) {
    var lamp = el("span", "lamp " + kind);
    lamp.appendChild(el("span", "d"));
    lamp.appendChild(document.createTextNode(label));
    return lamp;
  }

  function fmtMinutes(m) {
    return !m ? "—" : m + "m";
  }

  function pad2(n) {
    var s = String(n);
    return s.length < 2 ? "0" + s : s;
  }

  function whoIconClass(type) {
    if (type === "block") return "block";
    if (type === "known") return "num";
    return "any";
  }

  function whoGlyph(type) {
    if (type === "block") return "⦸"; // circled slash
    if (type === "known") return "☎"; // telephone
    return "✱"; // asterisk
  }

  function isBlockedRule(rule) {
    return !!(rule && rule.grant && rule.grant.tierId === BLOCK_TIER_ID);
  }

  /* ---------------- write-path helpers (same-origin only) ---------------- */

  // apiFetch issues a same-origin loopback request (relative path — never an
  // absolute/non-loopback URL) and resolves with the parsed JSON body plus
  // ok/status, regardless of HTTP status, so every caller can render the
  // server's structured {"error": "..."} APIError body on failure instead of
  // throwing past it.
  function apiFetch(method, path, body) {
    var opts = { method: method, headers: {} };
    if (body !== undefined) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function (resp) {
      return resp
        .json()
        .catch(function () {
          return null;
        })
        .then(function (json) {
          return { ok: resp.ok, status: resp.status, json: json };
        });
    });
  }

  function apiErrorMessage(result, fallback) {
    if (result && result.json && result.json.error) return result.json.error;
    return fallback || "Request failed (HTTP " + (result ? result.status : "?") + ")";
  }

  /* ---------------- toast (non-blocking write feedback) ---------------- */

  var toastTimer = null;
  function showToast(message, isError) {
    var toast = $("#toast");
    if (!toast) return;
    $("#toastMsg").textContent = message;
    toast.classList.toggle("err", !!isError);
    toast.classList.add("show");
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(function () {
      toast.classList.remove("show");
    }, 3200);
  }

  /* ---------------- compiles-to store-tag rendering (RULE-05) ---------------- */

  // storeInfo maps a compilesTo string (e.g. "DynamoDB (Tier item, joined by
  // AccessCode.tierId)") to a short CSS class + label for the store chip.
  // The classification is prefix-based against the server's own
  // compilesToMap() (view.go) — never a client-side guess at what field maps
  // to what store; if the server's map ever grows a new store name, this
  // falls back to a neutral "store" chip with the label taken verbatim
  // rather than guessing wrong.
  function storeInfo(label) {
    if (!label) return { cls: "", abbr: "" };
    var l = label.toLowerCase();
    if (l.indexOf("dynamodb") === 0) return { cls: "dynamo", abbr: "dynamo" };
    if (l.indexOf("toml") === 0) return { cls: "toml", abbr: "toml" };
    if (l.indexOf("yaml") === 0) return { cls: "yaml", abbr: "yaml" };
    if (l.indexOf("ssm") === 0) return { cls: "ssm", abbr: "ssm" };
    if (l.indexOf("voip.ms") === 0) return { cls: "voipms", abbr: "voip.ms" };
    return { cls: "", abbr: label.split(" ")[0].toLowerCase() };
  }

  function storeChip(label) {
    var info = storeInfo(label);
    return text("span", "store " + info.cls, info.abbr);
  }

  /* ---------------- error banner ---------------- */

  function showError(message, region, profile) {
    var banner = $("#errorBanner");
    var msgEl = $("#errorBannerText");
    var parts = [];
    if (message) parts.push(message);
    var loc = [];
    if (region) loc.push("region " + region);
    if (profile) loc.push("profile " + profile);
    if (loc.length) parts.push("(" + loc.join(", ") + ")");
    msgEl.textContent = parts.length
      ? parts.join(" ")
      : "kv studio could not load the live configuration.";
    banner.hidden = false;
  }

  function hideError() {
    $("#errorBanner").hidden = true;
  }

  /* ---------------- summary strip ---------------- */

  function renderSummary(cfg) {
    var strip = $("#summaryStrip");
    strip.textContent = "";
    var rules = cfg.rules || [];
    var dids = cfg.dids || [];
    var knowledge = cfg.knowledge || [];

    var maxConcurrent = rules.reduce(function (acc, r) {
      var c = (r.grant && r.grant.concurrency) || 0;
      return c > acc ? c : acc;
    }, 0);
    var blockedCount = rules.filter(function (r) {
      return r.who && r.who.type === "block";
    }).length;
    var openCount = rules.filter(function (r) {
      return (
        (!r.who || r.who.type !== "block") &&
        (!r.secret || r.secret.mode === "none")
      );
    }).length;
    var gatedCount = rules.length - blockedCount - openCount;
    var publicPacks = knowledge.filter(function (k) {
      return k.talkable;
    }).length;
    var hiddenPacks = knowledge.length - publicPacks;

    function stat(k, label, sub) {
      var s = el("div", "stat");
      s.appendChild(text("div", "k", String(k)));
      s.appendChild(text("div", "l eyebrow", label));
      if (sub) s.appendChild(text("div", "sub", sub));
      return s;
    }

    strip.appendChild(
      stat(
        dids.length,
        "DIDs mapped",
        dids.length
          ? dids.map(function (d) { return d.phone; }).join(" · ")
          : "none imported"
      )
    );
    strip.appendChild(
      stat(
        rules.length,
        "Routing rules",
        gatedCount + " gated · " + openCount + " open · " + blockedCount + " blocked"
      )
    );
    strip.appendChild(
      stat(
        knowledge.length,
        "Knowledge packs",
        publicPacks + " public · " + hiddenPacks + " hidden"
      )
    );
    strip.appendChild(stat(maxConcurrent, "Max concurrent", "across imported rules"));
  }

  /* ---------------- rules table ---------------- */

  function whoCell(who) {
    var wrap = el("div", "who");
    var type = (who && who.type) || "any";
    wrap.appendChild(text("span", "ic " + whoIconClass(type), whoGlyph(type)));
    var lbl = el("span", "lbl");
    var numbers =
      who && who.numbers && who.numbers.length
        ? who.numbers.join(", ")
        : type === "any"
        ? "any caller"
        : "—";
    lbl.appendChild(document.createTextNode(numbers));
    lbl.appendChild(text("small", "", type));
    wrap.appendChild(lbl);
    return wrap;
  }

  function secretCell(secret) {
    var wrap = el("div", "secret");
    if (!secret || secret.mode === "none") {
      wrap.appendChild(text("span", "none", "no secret"));
      return wrap;
    }
    wrap.appendChild(text("span", "ref", secret.ref || "(no ref)"));
    var modeLabel =
      secret.mode === "passphrase"
        ? "spoken passphrase"
        : secret.mode === "dtmf"
        ? "DTMF pin"
        : secret.mode === "either"
        ? "passphrase or pin"
        : secret.mode;
    wrap.appendChild(text("small", "", modeLabel));
    return wrap;
  }

  function grantCell(grant, knowledgeIds) {
    var wrap = el("div", "grant");
    var timeStr = fmtMinutes(grant && grant.minutes);
    if (grant && grant.tierId) timeStr += " · " + grant.tierId;
    wrap.appendChild(text("span", "time", timeStr));
    if (grant && grant.tierId === BLOCK_TIER_ID) {
      var pill = el("span", "blockedpill");
      pill.appendChild(document.createTextNode("⦸ blocked — call dropped before the agent"));
      wrap.appendChild(pill);
    }
    var packs = el("span", "packs");
    if (knowledgeIds && knowledgeIds.length) {
      knowledgeIds.forEach(function (id) {
        packs.appendChild(text("span", "pchip", id));
      });
    } else {
      var none = text("span", "pchip", "none");
      none.style.opacity = "0.5";
      packs.appendChild(none);
    }
    wrap.appendChild(packs);
    return wrap;
  }

  // orderCell renders the position badge plus up/down reorder controls
  // (RULE-03). Reordering only changes the operator-facing authoring/
  // presentation order persisted via PUT /api/order — it is explicitly NOT
  // consulted by the runtime caller-id resolver (16-RESEARCH.md, WhoSpec
  // doc comments), which is why the hint under the table and this button's
  // title both say so.
  function orderCell(i, total) {
    var wrap = el("div", "ord");
    wrap.appendChild(text("span", "n", pad2(i + 1)));
    var btns = el("div", "ordbtns");
    var up = el("button", "ordbtn");
    up.type = "button";
    up.textContent = "▲";
    up.title = "Move up (authoring order only — does not change call routing)";
    up.disabled = i === 0;
    up.addEventListener("click", function (e) {
      e.stopPropagation();
      moveRule(i, -1);
    });
    var down = el("button", "ordbtn");
    down.type = "button";
    down.textContent = "▼";
    down.title = "Move down (authoring order only — does not change call routing)";
    down.disabled = i === total - 1;
    down.addEventListener("click", function (e) {
      e.stopPropagation();
      moveRule(i, 1);
    });
    btns.appendChild(up);
    btns.appendChild(down);
    wrap.appendChild(btns);
    return wrap;
  }

  function renderRules(cfg) {
    var body = $("#ruleBody");
    body.textContent = "";
    var rules = cfg.rules || [];

    if (!rules.length) {
      var emptyRow = el("tr");
      var emptyCell = el("td", "hint");
      emptyCell.colSpan = 6;
      emptyCell.textContent = "No routing rules imported from the live config.";
      emptyRow.appendChild(emptyCell);
      body.appendChild(emptyRow);
      return;
    }

    rules.forEach(function (r, i) {
      var tr = el("tr", "rulerow");
      tr.tabIndex = 0;
      tr.dataset.id = r.id;

      var tdOrd = el("td");
      tdOrd.appendChild(orderCell(i, rules.length));

      var tdWho = el("td");
      tdWho.appendChild(whoCell(r.who));

      var tdSecret = el("td");
      tdSecret.appendChild(secretCell(r.secret));

      var tdArrow = text("td", "arrow", "→");

      var tdGrant = el("td");
      tdGrant.appendChild(grantCell(r.grant, r.knowledge));

      var tdPersona = el("td");
      tdPersona.appendChild(text("span", "persona", r.persona || "—"));

      tr.appendChild(tdOrd);
      tr.appendChild(tdWho);
      tr.appendChild(tdSecret);
      tr.appendChild(tdArrow);
      tr.appendChild(tdGrant);
      tr.appendChild(tdPersona);
      tr.addEventListener("click", function () {
        openRuleDrawer(r.id);
      });
      tr.addEventListener("keydown", function (e) {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          openRuleDrawer(r.id);
        }
      });
      body.appendChild(tr);

      var unlocks = r.unlocks || [];
      unlocks.forEach(function (u, ui) {
        var isLast = i === rules.length - 1 && ui === unlocks.length - 1;
        var ur = el("tr", "unlockrow" + (isLast ? " last" : ""));
        var td = el("td");
        td.colSpan = 6;
        var span = el("span", "unlock");
        span.appendChild(document.createTextNode("⤷ mid-call, caller says "));
        span.appendChild(text("span", "mono", "“" + u.phrase + "”"));
        span.appendChild(document.createTextNode(" → unlock "));
        span.appendChild(text("span", "mono", "+" + (u.add || []).join(", ")));
        td.appendChild(span);
        ur.appendChild(td);
        body.appendChild(ur);
      });
    });
  }

  // moveRule swaps rule at index i with its neighbor (dir -1 up, +1 down) in
  // the CURRENT displayed order, then persists the full new order via
  // PUT /api/order (RULE-03: presentation order only). Re-fetches on success
  // so the table always reflects what the server actually persisted.
  function moveRule(i, dir) {
    var rules = (state.config && state.config.rules) || [];
    var j = i + dir;
    if (j < 0 || j >= rules.length) return;
    var order = rules.map(function (r) {
      return r.id;
    });
    var tmp = order[i];
    order[i] = order[j];
    order[j] = tmp;
    apiFetch("PUT", "/api/order", { order: order }).then(function (result) {
      if (!result.ok) {
        showToast(apiErrorMessage(result, "Could not save the new order."), true);
        return;
      }
      showToast("Order saved — presentation only, routing unchanged.");
      load();
    });
  }

  /* ---------------- rule-editor drawer (RULE-01/02/03/04/05) ---------------- */

  // fieldRow builds a labeled .field wrapper (label + input/control + an
  // optional hint line), the drawer's basic form-row unit.
  function fieldRow(labelText, control, hintText) {
    var wrap = el("div", "field");
    wrap.appendChild(text("span", "flab", labelText));
    wrap.appendChild(control);
    if (hintText) wrap.appendChild(text("span", "hint", hintText));
    return wrap;
  }

  // segControl is a small segmented-button radio group; onSelect(value) only
  // fires for an enabled control, and the caller re-renders the drawer to
  // reflect the new selection (keeps this file's single re-render pattern
  // rather than tracking per-button DOM diffing).
  function segControl(options, current, disabled, onSelect) {
    var seg = el("div", "seg");
    options.forEach(function (opt) {
      var b = el("button", opt.value === current ? "on" : "");
      b.type = "button";
      b.textContent = opt.label;
      b.disabled = !!disabled;
      if (!disabled) {
        b.addEventListener("click", function () {
          onSelect(opt.value);
        });
      }
      seg.appendChild(b);
    });
    return seg;
  }

  function cpLine(storeLabel, pathText, tailText) {
    var line = el("div", "cp-line");
    if (storeLabel) line.appendChild(storeChip(storeLabel));
    line.appendChild(text("span", "cp-path", pathText));
    if (tailText) line.appendChild(text("span", "cp-tail", tailText));
    return line;
  }

  // tierGrantMap builds tierId -> {minutes, periodMin, concurrency} from
  // every rule currently in cfg (every rule already carries its own
  // server-joined Tier limits) — the ONLY way this drawer can show
  // session/period/concurrency for a tier id, since there is no "list
  // tiers" or "read a tier's limits" endpoint in this build (only the
  // AccessCode.tierId pointer is writable here — see the file header note).
  function tierGrantMap(cfg) {
    var map = {};
    (cfg.rules || []).forEach(function (r) {
      if (r.grant && r.grant.tierId) map[r.grant.tierId] = r.grant;
    });
    return map;
  }

  function tierIdOptionsDatalist(cfg) {
    var dl = el("datalist");
    dl.id = "tierIdOptions";
    var seen = {};
    (cfg.rules || []).forEach(function (r) {
      var id = r.grant && r.grant.tierId;
      if (id && !seen[id]) {
        seen[id] = true;
        var opt = el("option");
        opt.value = id;
        dl.appendChild(opt);
      }
    });
    return dl;
  }

  // buildInitialFormState snapshots the drawer's editable fields from an
  // existing rule (edit mode) or sensible defaults (create mode). secretMode
  // uses the sentinel "no-secret" (never gate_mode="none" — 16-RESEARCH.md
  // Pitfall 2) to represent require_gate=false.
  function buildInitialFormState(cfg, rule) {
    if (rule) {
      var currentMode = (rule.secret && rule.secret.mode) || "passphrase";
      return {
        isNew: false,
        code: rule.id,
        whoType: rule.who && rule.who.type === "known" ? "known" : "any",
        phone: (rule.who && rule.who.numbers && rule.who.numbers[0]) || "",
        secretMode: currentMode,
        secretTouched: false,
        grantTierId: (rule.grant && rule.grant.tierId) || "",
        blocked: isBlockedRule(rule)
      };
    }
    var rules = cfg.rules || [];
    var globalMode = (rules[0] && rules[0].secret && rules[0].secret.mode) || "passphrase";
    return {
      isNew: true,
      code: "",
      whoType: "any",
      phone: "",
      secretMode: globalMode,
      secretTouched: false,
      grantTierId: "",
      blocked: false
    };
  }

  function currentRule() {
    if (!state.config || !state.drawerRuleId) return null;
    return (state.config.rules || []).find(function (r) {
      return r.id === state.drawerRuleId;
    }) || null;
  }

  var SECRET_OPTIONS = [
    { value: "passphrase", label: "Spoken passphrase" },
    { value: "dtmf", label: "DTMF pin" },
    { value: "either", label: "Either" },
    { value: "no-secret", label: "No secret required" }
  ];

  function renderDrawer() {
    var cfg = state.config;
    var form = state.drawerForm;
    if (!cfg || !form) return;
    var rule = currentRule();
    var compiles = cfg.compilesTo || {};

    $("#drawerEyebrow").textContent = form.isNew ? "New rule" : "Editing rule";
    $("#drawerTitle").textContent = form.isNew
      ? "unsaved"
      : form.code + (form.blocked ? " — blocked" : "");

    var body = $("#drawerBody");
    body.textContent = "";

    renderWhoStep(body, form, rule, compiles);
    renderSecretStep(body, cfg, form, compiles);
    renderGrantStep(body, cfg, form, rule, compiles);
    renderKnowledgeStep(body, cfg, rule, compiles);
    renderPersonaStep(body, rule);
    renderCompilesToSummary(body, form, rule, compiles);

    // footer button visibility
    var canDelete = !form.isNew;
    var canBlock = !form.isNew && !form.blocked;
    $("#drawerDelete").hidden = !canDelete;
    $("#drawerBlock").hidden = !canBlock;
  }

  // renderWhoStep, and every renderXStep function below it, appends one
  // <div class="flowstep"> section to container — the whole drawer body is
  // rebuilt from state.drawerForm on every field change (matches this
  // file's existing render-from-state style; simpler and safer than
  // incremental DOM patching for a form this size).
  function renderWhoStep(container, form, rule, compiles) {
    var step = el("div", "flowstep");
    var head = el("div", "head");
    head.appendChild(text("span", "fx", "1"));
    head.appendChild(text("h4", "", "Who calls"));
    var compilesWrap = el("span", "compiles");
    compilesWrap.appendChild(storeChip(compiles["rule.who"]));
    head.appendChild(compilesWrap);
    step.appendChild(head);

    var editableWho = form.isNew;
    var seg = segControl(
      [
        { value: "known", label: "Known number" },
        { value: "any", label: "Any / unknown caller" }
      ],
      form.whoType,
      !editableWho,
      function (v) {
        form.whoType = v;
        renderDrawer();
      }
    );
    var whoField = el("div", "field");
    whoField.appendChild(seg);
    step.appendChild(whoField);

    var phoneInput = el("input", "input");
    phoneInput.type = "text";
    phoneInput.placeholder = "+1 613 555 0100";
    phoneInput.value = form.phone;
    phoneInput.disabled = !editableWho || form.whoType !== "known";
    phoneInput.addEventListener("input", function (e) {
      form.phone = e.target.value;
    });
    var whoHint = editableWho
      ? "Resolved against the sparse byPhone index at call time."
      : "This build has no endpoint to edit an existing rule's caller-ID mapping — delete and recreate the rule to change WHO.";
    step.appendChild(fieldRow("E.164 number", phoneInput, whoHint));

    if (!form.isNew && form.blocked) {
      step.appendChild(text("div", "hint", "⦸ This rule is blocked — the call is dropped before it reaches the agent."));
    }

    container.appendChild(step);
  }

  function renderSecretStep(container, cfg, form, compiles) {
    var step = el("div", "flowstep");
    var head = el("div", "head");
    head.appendChild(text("span", "fx", "2"));
    head.appendChild(text("h4", "", "Secret"));
    var compilesWrap = el("span", "compiles");
    compilesWrap.appendChild(storeChip(compiles["rule.secret.mode"]));
    compilesWrap.appendChild(storeChip(compiles["rule.secret.requireGate"]));
    head.appendChild(compilesWrap);
    step.appendChild(head);

    step.appendChild(
      text(
        "div",
        "hint",
        "Shared gate config — this applies to every rule, not just this one (v1 has one telephony gate for the whole console)."
      )
    );

    var seg = segControl(SECRET_OPTIONS, form.secretMode, false, function (v) {
      form.secretMode = v;
      form.secretTouched = true;
      renderDrawer();
    });
    var segField = el("div", "field");
    segField.appendChild(seg);
    step.appendChild(segField);

    if (form.secretMode === "no-secret") {
      step.appendChild(text("span", "hint", "Open route — no gate. require_gate will be written as false."));
    } else {
      step.appendChild(
        text(
          "span",
          "hint",
          "Selecting a mode writes gate_mode=\"" + form.secretMode + "\" and require_gate=true. This never writes the secret VALUE — that's Phase 17."
        )
      );
    }

    var refText = "(no ref configured)";
    var rule = currentRule();
    if (rule && rule.secret && rule.secret.ref) refText = rule.secret.ref;
    else if (cfg.rules && cfg.rules[0] && cfg.rules[0].secret && cfg.rules[0].secret.ref) refText = cfg.rules[0].secret.ref;
    var refWrap = el("div", "field");
    refWrap.appendChild(text("span", "flab", "Secret reference (SSM param name)"));
    var refDisplay = el("div", "input");
    refDisplay.style.opacity = "0.75";
    refDisplay.appendChild(document.createTextNode(refText));
    refWrap.appendChild(refDisplay);
    refWrap.appendChild(text("span", "hint", "Name only — never a value. Rotating/writing the actual secret is Phase 17."));
    step.appendChild(refWrap);

    container.appendChild(step);
  }

  function renderGrantStep(container, cfg, form, rule, compiles) {
    var step = el("div", "flowstep");
    var head = el("div", "head");
    head.appendChild(text("span", "fx", "3"));
    head.appendChild(text("h4", "", "Grant · time"));
    var compilesWrap = el("span", "compiles");
    compilesWrap.appendChild(storeChip(compiles["rule.grant"]));
    head.appendChild(compilesWrap);
    step.appendChild(head);

    if (form.isNew) {
      var codeInput = el("input", "input");
      codeInput.type = "text";
      codeInput.value = form.code;
      codeInput.placeholder = "e.g. recruiting-30";
      codeInput.addEventListener("input", function (e) {
        form.code = e.target.value;
      });
      step.appendChild(fieldRow("Code (rule id)", codeInput, "The AccessCode identifier — becomes the /api/rules/{code} path."));
    } else {
      var codeDisplay = el("div", "input");
      codeDisplay.style.opacity = "0.75";
      codeDisplay.appendChild(document.createTextNode(form.code));
      step.appendChild(fieldRow("Code (rule id)", codeDisplay));
    }

    var tierInput = el("input", "input");
    tierInput.type = "text";
    tierInput.value = form.grantTierId;
    tierInput.setAttribute("list", "tierIdOptions");
    tierInput.placeholder = "tier id";
    var tierMap = tierGrantMap(cfg);
    tierInput.addEventListener("input", function (e) {
      form.grantTierId = e.target.value;
      var known = tierMap[form.grantTierId];
      $("#grantMinutes").textContent = known ? fmtMinutes(known.minutes) : "—";
      $("#grantPeriod").textContent = known ? fmtMinutes(known.periodMin) : "—";
      $("#grantConcurrency").textContent = known ? String(known.concurrency) : "—";
    });
    step.appendChild(fieldRow("Tier id", tierInput, "Repoints this rule's grant at an existing Tier row — see `kv tier list`."));

    var grid = el("div", "grantgrid");
    var known = tierMap[form.grantTierId];
    var minutesField = fieldRow("Session", text("div", "input", known ? fmtMinutes(known.minutes) : "—"));
    grid.appendChild(minutesField);
    var periodField = fieldRow("Period cap", text("div", "input", known ? fmtMinutes(known.periodMin) : "—"));
    grid.appendChild(periodField);
    var concField = fieldRow("Concurrent", text("div", "input", known ? String(known.concurrency) : "—"));
    grid.appendChild(concField);
    step.appendChild(grid);
    step.appendChild(
      text(
        "span",
        "hint",
        "Session/period/concurrency are read-only here — this build has no endpoint to write a Tier's limits directly (only which tier a rule points at)."
      )
    );

    // Give the three read-only spans stable ids (built via fieldRow, which
    // nests the value directly as the .field's second child — select by
    // structure) BEFORE inserting into the document, so the tierInput
    // "input" listener above can find them by id afterward.
    var grantDivs = grid.querySelectorAll(".field > .input");
    if (grantDivs[0]) grantDivs[0].id = "grantMinutes";
    if (grantDivs[1]) grantDivs[1].id = "grantPeriod";
    if (grantDivs[2]) grantDivs[2].id = "grantConcurrency";

    container.appendChild(tierIdOptionsDatalist(cfg));
    container.appendChild(step);
  }

  function renderKnowledgeStep(container, cfg, rule, compiles) {
    var step = el("div", "flowstep");
    var head = el("div", "head");
    head.appendChild(text("span", "fx", "4"));
    head.appendChild(text("h4", "", "Knowledge scope"));
    var compilesWrap = el("span", "compiles");
    compilesWrap.appendChild(storeChip(compiles["rule.knowledge"]));
    var readOnlyBadge = text("span", "hint", "read-only — v2");
    compilesWrap.appendChild(readOnlyBadge);
    head.appendChild(compilesWrap);
    step.appendChild(head);

    var ids = (rule && rule.knowledge) || cfg.knowledge && cfg.knowledge.map(function (k) { return k.id; }) || [];
    var chips = el("div", "chips");
    if (ids.length) {
      ids.forEach(function (id) {
        chips.appendChild(text("span", "chip", id));
      });
    } else {
      chips.appendChild(text("span", "chip", "none"));
    }
    step.appendChild(fieldRow("Reachable packs", chips, "Every rule reaches every knowledge pack in v1 — per-rule binding is a future phase, not editable here."));

    container.appendChild(step);
  }

  function renderPersonaStep(container, rule) {
    var step = el("div", "flowstep");
    var head = el("div", "head");
    head.appendChild(text("span", "fx", "5"));
    head.appendChild(text("h4", "", "Persona"));
    var badge = el("span", "compiles");
    badge.appendChild(text("span", "hint", "read-only — v2"));
    head.appendChild(badge);
    step.appendChild(head);

    var personaDisplay = el("div", "input");
    personaDisplay.style.opacity = "0.75";
    personaDisplay.appendChild(document.createTextNode((rule && rule.persona) || "concierge"));
    step.appendChild(fieldRow("Active persona", personaDisplay, "Single fixed persona in v1 — per-rule persona binding is a future phase, not editable here."));

    container.appendChild(step);
  }

  function renderCompilesToSummary(container, form, rule, compiles) {
    var panel = el("div", "compiles-panel");
    panel.appendChild(text("div", "cp-head eyebrow", "This rule compiles to"));
    panel.appendChild(
      cpLine(
        compiles["rule.who"],
        compiles["rule.who"] || "AccessCode.phone / gsi3pk",
        form.whoType === "known" ? form.phone || "(enter a number)" : "any caller — no phone mapping"
      )
    );
    panel.appendChild(
      cpLine(
        compiles["rule.grant"],
        compiles["rule.grant"] || "Tier item, joined by AccessCode.tierId",
        form.grantTierId || "(enter a tier id)"
      )
    );
    panel.appendChild(
      cpLine(
        compiles["rule.secret.mode"],
        compiles["rule.secret.mode"] || "telephony.toml gate_mode",
        form.secretMode === "no-secret" ? "unused — no gate" : form.secretMode
      )
    );
    panel.appendChild(
      cpLine(
        compiles["rule.secret.requireGate"],
        compiles["rule.secret.requireGate"] || "telephony.toml require_gate",
        form.secretTouched ? String(form.secretMode !== "no-secret") : "unchanged (this build can't read the current value)"
      )
    );
    panel.appendChild(
      cpLine(compiles["rule.unlocks"], compiles["rule.unlocks"] || "topic-map.yaml keywords", ((rule && rule.unlocks) || []).length + " phrase(s), shared")
    );
    panel.appendChild(
      cpLine(compiles["rule.knowledge"], compiles["rule.knowledge"] || "manifest.yaml", ((rule && rule.knowledge) || []).length + " packs, shared")
    );
    container.appendChild(panel);
  }

  function openRuleDrawer(ruleId) {
    var cfg = state.config;
    if (!cfg) return;
    var rule = null;
    if (ruleId) {
      rule = (cfg.rules || []).find(function (r) {
        return r.id === ruleId;
      });
      if (!rule) return;
    }
    state.drawerRuleId = ruleId || null;
    state.drawerForm = buildInitialFormState(cfg, rule);
    renderDrawer();

    $all(".rulerow").forEach(function (tr) {
      tr.classList.toggle("sel", tr.dataset.id === ruleId);
    });

    var scrim = $("#scrim");
    var drawer = $("#drawer");
    scrim.hidden = false;
    drawer.hidden = false;
    // rAF so the browser registers the un-hidden state before the
    // transform transition kicks in.
    window.requestAnimationFrame(function () {
      scrim.classList.add("open");
      drawer.classList.add("open");
    });
    drawer.setAttribute("aria-hidden", "false");
  }

  function closeRuleDrawer() {
    var scrim = $("#scrim");
    var drawer = $("#drawer");
    scrim.classList.remove("open");
    drawer.classList.remove("open");
    drawer.setAttribute("aria-hidden", "true");
    setTimeout(function () {
      scrim.hidden = true;
      drawer.hidden = true;
    }, 240);
    state.drawerRuleId = null;
    state.drawerForm = null;
    $all(".rulerow").forEach(function (tr) {
      tr.classList.remove("sel");
    });
  }

  // saveSecretIfTouched issues PUT /api/secret only when the operator
  // actually interacted with the SECRET segmented control this session —
  // SECRET is a shared, global config (16-RESEARCH.md), so an untouched
  // drawer must never overwrite it with a guessed value.
  function saveSecretIfTouched(form) {
    if (!form.secretTouched) return Promise.resolve({ ok: true });
    var req =
      form.secretMode === "no-secret"
        ? { requireGate: false }
        : { gateMode: form.secretMode, requireGate: true };
    return apiFetch("PUT", "/api/secret", req);
  }

  function saveRuleDrawer() {
    var form = state.drawerForm;
    if (!form) return;

    if (form.isNew) {
      if (!form.code.trim()) {
        showToast("Code is required for a new rule.", true);
        return;
      }
      if (!form.grantTierId.trim()) {
        showToast("Tier id is required for a new rule.", true);
        return;
      }
      var createReq = { code: form.code.trim(), tierId: form.grantTierId.trim() };
      if (form.whoType === "known" && form.phone.trim()) createReq.phone = form.phone.trim();
      apiFetch("POST", "/api/rules", createReq).then(function (result) {
        if (!result.ok) {
          showToast(apiErrorMessage(result, "Could not create the rule."), true);
          return;
        }
        saveSecretIfTouched(form).then(function (secretResult) {
          if (!secretResult.ok) {
            showToast(apiErrorMessage(secretResult, "Rule created, but the secret config failed to save."), true);
          } else {
            showToast("Rule " + form.code.trim() + " created.");
          }
          closeRuleDrawer();
          load();
        });
      });
      return;
    }

    if (!form.grantTierId.trim()) {
      showToast("Tier id is required.", true);
      return;
    }
    apiFetch("PUT", "/api/rules/" + encodeURIComponent(form.code), { tierId: form.grantTierId.trim() }).then(function (result) {
      if (!result.ok) {
        showToast(apiErrorMessage(result, "Could not save the rule."), true);
        return;
      }
      saveSecretIfTouched(form).then(function (secretResult) {
        if (!secretResult.ok) {
          showToast(apiErrorMessage(secretResult, "Grant saved, but the secret config failed to save."), true);
        } else {
          showToast("Rule " + form.code + " saved.");
        }
        closeRuleDrawer();
        load();
      });
    });
  }

  function deleteRuleDrawer() {
    var form = state.drawerForm;
    if (!form || form.isNew) return;
    if (!window.confirm("Delete rule \"" + form.code + "\"? This cannot be undone.")) return;
    apiFetch("DELETE", "/api/rules/" + encodeURIComponent(form.code)).then(function (result) {
      if (!result.ok) {
        showToast(apiErrorMessage(result, "Could not delete the rule."), true);
        return;
      }
      showToast("Rule " + form.code + " deleted.");
      closeRuleDrawer();
      load();
    });
  }

  function blockRuleDrawer() {
    var form = state.drawerForm;
    if (!form || form.isNew) return;
    var who = form.whoType === "known" && form.phone ? form.phone : form.code;
    if (
      !window.confirm(
        "Block " + who + "? The call will be dropped before it reaches the agent. This does not delete the rule or its phone mapping."
      )
    )
      return;
    apiFetch("POST", "/api/rules/" + encodeURIComponent(form.code) + "/block").then(function (result) {
      if (!result.ok) {
        showToast(apiErrorMessage(result, "Could not block this number."), true);
        return;
      }
      showToast(form.code + " blocked — call dropped before the agent.");
      closeRuleDrawer();
      load();
    });
  }

  /* ---------------- DIDs ---------------- */

  function renderDids(cfg) {
    var list = $("#didList");
    list.textContent = "";
    var dids = cfg.dids || [];

    if (!dids.length) {
      list.appendChild(text("div", "hint", "No DIDs imported from the live config."));
      return;
    }

    dids.forEach(function (d) {
      var row = el("div", "didrow");
      row.appendChild(text("span", "ic num", "☎"));
      var body = el("div");
      body.appendChild(text("div", "num", d.phone));
      body.appendChild(
        text("div", "dl", "code " + d.code + " · tier " + d.tierId)
      );
      row.appendChild(body);
      var da = el("div", "da");
      da.appendChild(lampEl(d.enabled ? "live" : "block", d.enabled ? "enabled" : "disabled"));
      row.appendChild(da);
      list.appendChild(row);
    });
  }

  /* ---------------- DID manager modal (DID-01/02) ---------------- */
  //
  // Distinct from renderDids() above: that renders cfg.dids, the §23
  // caller-ID mint mapping (phone -> access code, keyed by the CALLER's
  // number). This modal manages cfg.inboundDids / GET /api/dids — the
  // numbers the PUBLIC dials INTO, merged from live VoIP.ms state and
  // studio-owned dids.yaml metadata (default rule + opening greeting).
  // "Add" always routes an already-owned VoIP.ms number — there is no
  // "purchase a new number" affordance anywhere in this modal
  // (16-RESEARCH.md Pitfall 3).

  function ruleOptionsDatalist(cfg) {
    var dl = el("datalist");
    dl.id = "ruleIdOptions";
    (cfg.rules || []).forEach(function (r) {
      var opt = el("option");
      opt.value = r.id;
      dl.appendChild(opt);
    });
    return dl;
  }

  function inboundDidRow(d) {
    var row = el("div", "didrow");
    row.appendChild(text("span", "ic num", "☎"));
    var body = el("div");
    body.style.minWidth = "0";
    body.appendChild(text("div", "num", d.did));
    var labelBits = [];
    if (d.label) labelBits.push(d.label);
    if (d.routing) labelBits.push(d.routing);
    body.appendChild(text("div", "dl", labelBits.length ? labelBits.join(" · ") : "no VoIP.ms label"));
    var meta = el("div", "didmeta");
    var greetSpan = el("span");
    greetSpan.appendChild(document.createTextNode("opens with "));
    greetSpan.appendChild(text("span", "val", d.greeting || "(default greeting)"));
    meta.appendChild(greetSpan);
    var ruleSpan = el("span");
    ruleSpan.appendChild(document.createTextNode("default → "));
    ruleSpan.appendChild(text("span", "rulechip", d.defaultRule || "(none set)"));
    meta.appendChild(ruleSpan);
    body.appendChild(meta);
    row.appendChild(body);

    var da = el("div", "da");
    if (d.region) da.appendChild(text("span", "region", d.region));
    var editBtn = el("button", "btn ghost btnedit");
    editBtn.type = "button";
    editBtn.appendChild(text("span", "k", "✎"));
    editBtn.appendChild(document.createTextNode(" Edit"));
    editBtn.addEventListener("click", function () {
      showDidForm("edit", d);
    });
    da.appendChild(editBtn);
    row.appendChild(da);
    return row;
  }

  function renderDidModalList(filterText) {
    var wrap = $("#didModalList");
    wrap.textContent = "";
    var q = (filterText || "").trim().toLowerCase();
    var rows = (state.dids || []).filter(function (d) {
      if (!q) return true;
      var hay = ((d.did || "") + (d.label || "") + (d.region || "")).toLowerCase();
      return hay.indexOf(q) !== -1;
    });

    if (!rows.length) {
      wrap.appendChild(text("div", "hint", q ? "No DIDs match “" + filterText + "”." : "No inbound DIDs yet."));
      return;
    }
    rows.forEach(function (d) {
      wrap.appendChild(inboundDidRow(d));
    });
  }

  function renderDidStatusNote() {
    var note = $("#didStatusNote");
    note.textContent = "";
    if (!state.didsStatus) return;
    var box = el("div", "note disabled-note");
    box.appendChild(text("span", "i", "⚠"));
    var msg = el("div");
    msg.appendChild(document.createTextNode("VoIP.ms live list unavailable (" + state.didsStatus + "). Studio-owned metadata below is still shown and editable."));
    box.appendChild(msg);
    note.appendChild(box);
  }

  function fetchAndRenderDids() {
    return apiFetch("GET", "/api/dids").then(function (result) {
      if (result.ok && result.json) {
        state.dids = result.json.dids || [];
        state.didsStatus = result.json.status || "";
      } else {
        state.dids = [];
        state.didsStatus = "error — could not load /api/dids";
      }
      renderDidStatusNote();
      renderDidModalList($("#didSearch").value);
    });
  }

  function openDidModal() {
    hideDidForm();
    $("#didSearch").value = "";
    var scrim = $("#didScrim");
    var modal = $("#didModal");
    scrim.hidden = false;
    modal.hidden = false;
    window.requestAnimationFrame(function () {
      scrim.classList.add("open");
      modal.classList.add("open");
    });
    fetchAndRenderDids().then(function () {
      $("#didSearch").focus();
    });
  }

  function closeDidModal() {
    var scrim = $("#didScrim");
    var modal = $("#didModal");
    scrim.classList.remove("open");
    modal.classList.remove("open");
    setTimeout(function () {
      scrim.hidden = true;
      modal.hidden = true;
    }, 180);
    hideDidForm();
  }

  // showDidForm renders the shared add/edit form inline in the modal.
  // mode "add": Did is a fresh, editable E.164 field — POST /api/dids
  // routes an already-owned number + writes metadata (16-RESEARCH.md
  // Pitfall 3: never a "purchase" call, no such endpoint exists).
  // mode "edit": Did is the path key (read-only) — PUT /api/dids/{did}
  // upserts metadata only, never re-routes.
  function showDidForm(mode, did) {
    state.didFormMode = mode;
    state.didFormTarget = did || null;
    var cfg = state.config || {};

    var wrap = $("#didFormWrap");
    wrap.textContent = "";
    wrap.hidden = false;
    $("#didModalList").hidden = true;
    $all(".searchbox").forEach(function (s) {
      s.hidden = true;
    });

    if (mode === "add") {
      var addNote = el("div", "did-add-note");
      addNote.appendChild(text("span", "i", "ℹ"));
      addNote.appendChild(
        document.createTextNode(
          "Routes a VoIP.ms number your account already owns to this console, then saves its label/region/default-rule/greeting. This does NOT purchase a new number."
        )
      );
      wrap.appendChild(addNote);
    }

    var didInput = el("input", "input");
    didInput.type = "text";
    didInput.placeholder = "+1 613 555 0142";
    didInput.value = (did && did.did) || "";
    didInput.disabled = mode === "edit";
    wrap.appendChild(fieldRow("DID (E.164)", didInput, mode === "edit" ? "The DID number itself isn't editable — it's the record's identity." : "Must already be owned in your VoIP.ms account."));

    var labelInput = el("input", "input");
    labelInput.type = "text";
    labelInput.value = (did && did.label) || "";
    labelInput.placeholder = "e.g. Ottawa · primary";
    wrap.appendChild(fieldRow("Label", labelInput));

    var regionInput = el("input", "input");
    regionInput.type = "text";
    regionInput.value = (did && did.region) || "";
    regionInput.placeholder = "e.g. Ottawa, ON";
    wrap.appendChild(fieldRow("Region", regionInput));

    wrap.appendChild(ruleOptionsDatalist(cfg));
    var ruleInput = el("input", "input");
    ruleInput.type = "text";
    ruleInput.value = (did && did.defaultRule) || "";
    ruleInput.setAttribute("list", "ruleIdOptions");
    ruleInput.placeholder = "existing rule id";
    var compiles = cfg.compilesTo || {};
    wrap.appendChild(fieldRow("Default rule", ruleInput, "compiles to " + (compiles["did.defaultRule"] || "dids.yaml")));

    var greetingInput = el("input", "input");
    greetingInput.type = "text";
    greetingInput.value = (did && did.greeting) || "";
    greetingInput.placeholder = "e.g. standard concierge open";
    wrap.appendChild(fieldRow("Opening greeting", greetingInput, "compiles to " + (compiles["did.greeting"] || "dids.yaml")));

    var actions = el("div", "did-form-actions");
    var cancelBtn = el("button", "btn ghost");
    cancelBtn.type = "button";
    cancelBtn.textContent = "Cancel";
    cancelBtn.addEventListener("click", hideDidForm);
    var saveBtn = el("button", "btn primary");
    saveBtn.type = "button";
    saveBtn.textContent = mode === "add" ? "Route + save" : "Save";
    saveBtn.addEventListener("click", function () {
      submitDidForm({
        did: didInput.value.trim(),
        label: labelInput.value.trim(),
        region: regionInput.value.trim(),
        defaultRule: ruleInput.value.trim(),
        greeting: greetingInput.value.trim()
      });
    });
    actions.appendChild(cancelBtn);
    actions.appendChild(saveBtn);
    wrap.appendChild(actions);
  }

  function hideDidForm() {
    state.didFormMode = null;
    state.didFormTarget = null;
    var wrap = $("#didFormWrap");
    if (wrap) {
      wrap.hidden = true;
      wrap.textContent = "";
    }
    var list = $("#didModalList");
    if (list) list.hidden = false;
    $all(".searchbox").forEach(function (s) {
      s.hidden = false;
    });
  }

  function submitDidForm(fields) {
    if (!fields.did) {
      showToast("A DID number is required.", true);
      return;
    }
    var mode = state.didFormMode;
    var promise =
      mode === "edit"
        ? apiFetch("PUT", "/api/dids/" + encodeURIComponent(fields.did), fields)
        : apiFetch("POST", "/api/dids", fields);

    promise.then(function (result) {
      if (!result.ok) {
        showToast(apiErrorMessage(result, "Could not save this DID."), true);
        return;
      }
      var note = result.json && result.json.routingNote;
      showToast(note ? fields.did + " saved — " + note : fields.did + " saved.");
      hideDidForm();
      fetchAndRenderDids();
      load();
    });
  }

  /* ---------------- knowledge ---------------- */

  // fmtTokens renders KnowledgePack.tokenEstimate (a cheap len(bytes)/4
  // estimate — never a live token-count API call, see types.go) as a short
  // human string. 0 (a not-yet-built pack) reads as "not built yet" rather
  // than "0 tokens", which would misleadingly imply an empty pack.
  function fmtTokens(n) {
    if (!n) return "not built yet";
    if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, "") + "k tokens (est.)";
    return n + " tokens (est.)";
  }

  // renderAddSourceForm builds the collapsed "+ Add source" affordance for
  // one pack: a toggle button, and a path + kind form that POSTs
  // {topicId, path, kind} to /api/manifest/sources (KNOW-02) on submit. A
  // bad kind or unknown topic id comes back as the server's structured
  // APIError and is shown inline via errEl — never a silent failure, never
  // an alert(). On success the form resets, closes, and the whole console
  // re-fetches /api/config so the new source shows up immediately.
  function renderAddSourceForm(p) {
    var wrap = el("div", "addsource");

    var toggleBtn = el("button", "btn ghost addsource-toggle");
    toggleBtn.type = "button";
    toggleBtn.appendChild(text("span", "k", "+"));
    toggleBtn.appendChild(document.createTextNode(" Add source"));

    var formWrap = el("div", "addsource-form");
    formWrap.hidden = true;

    var pathInput = el("input", "input");
    pathInput.type = "text";
    pathInput.placeholder = "e.g. docs/some-doc.md";

    var kindSelect = el("select", "selectish");
    MANIFEST_SOURCE_KINDS.forEach(function (k) {
      var opt = el("option");
      opt.value = k;
      opt.textContent = k;
      kindSelect.appendChild(opt);
    });

    var errEl = text("div", "addsource-err", "");
    errEl.hidden = true;

    var addBtn = el("button", "btn primary");
    addBtn.type = "button";
    addBtn.textContent = "Add";
    var cancelBtn = el("button", "btn ghost");
    cancelBtn.type = "button";
    cancelBtn.textContent = "Cancel";

    function closeForm() {
      formWrap.hidden = true;
      toggleBtn.hidden = false;
      pathInput.value = "";
      errEl.hidden = true;
      errEl.textContent = "";
    }

    toggleBtn.addEventListener("click", function () {
      formWrap.hidden = false;
      toggleBtn.hidden = true;
      pathInput.focus();
    });
    cancelBtn.addEventListener("click", closeForm);

    addBtn.addEventListener("click", function () {
      var path = pathInput.value.trim();
      if (!path) {
        errEl.textContent = "Path is required.";
        errEl.hidden = false;
        return;
      }
      addBtn.disabled = true;
      apiFetch("POST", "/api/manifest/sources", {
        topicId: p.id,
        path: path,
        kind: kindSelect.value
      }).then(function (result) {
        addBtn.disabled = false;
        if (!result.ok) {
          errEl.textContent = apiErrorMessage(result, "Could not add this source.");
          errEl.hidden = false;
          return;
        }
        showToast("Source added to " + (p.spokenName || p.id) + " — rebuild to regenerate the pack.");
        closeForm();
        load();
      });
    });

    var actions = el("div", "addsource-actions");
    actions.appendChild(addBtn);
    actions.appendChild(cancelBtn);

    formWrap.appendChild(fieldRow("Path", pathInput));
    formWrap.appendChild(fieldRow("Kind", kindSelect));
    formWrap.appendChild(errEl);
    formWrap.appendChild(actions);

    wrap.appendChild(toggleBtn);
    wrap.appendChild(formWrap);
    return wrap;
  }

  function renderPacks(cfg) {
    var wrap = $("#packCards");
    wrap.textContent = "";
    var packs = cfg.knowledge || [];

    if (!packs.length) {
      wrap.appendChild(
        text("div", "hint", "No knowledge packs imported from the live config.")
      );
      return;
    }

    packs.forEach(function (p) {
      var card = el("div", "card");
      var h3 = el("h3");
      h3.appendChild(document.createTextNode((p.spokenName || p.id) + " "));
      h3.appendChild(lampEl(p.talkable ? "live" : "gated", p.talkable ? "talkable" : "hidden"));
      if (p.kind) {
        var kindWrap = el("span", "kind");
        kindWrap.appendChild(text("span", "kindtag", p.kind));
        h3.appendChild(kindWrap);
      }
      card.appendChild(h3);

      var srcs = el("div", "srcs");
      (p.sources || []).forEach(function (s) {
        var srcRow = el("div", "src");
        srcRow.appendChild(text("span", "kindtag", s.kind || "source"));
        srcRow.appendChild(
          document.createTextNode(" " + s.path + (s.public ? "" : " (private)"))
        );
        srcs.appendChild(srcRow);
      });
      card.appendChild(srcs);

      var foot = el("div", "foot");
      var uses = p.usedByRules || 0;
      var footText = uses + " rule" + (uses === 1 ? "" : "s") + " · " + fmtTokens(p.tokenEstimate);
      if (p.pack) footText += " · pack " + p.pack;
      foot.appendChild(text("span", "", footText));
      foot.appendChild(text("span", "store yaml", "manifest.yaml"));
      card.appendChild(foot);

      card.appendChild(renderAddSourceForm(p));

      wrap.appendChild(card);
    });
  }

  // renderRebuildResult renders POST /api/knowledge/rebuild's RebuildResult
  // (KNOW-03): the short "N packs changed → review the diff" summary, the
  // read-only git diff --stat, the changed-file list, and — on a non-zero
  // subprocess exit — the subprocess's stderr VERBATIM (never collapsed to
  // a generic message). The trailing note reiterates D-09's human-review
  // gate: this call never runs git add/commit.
  function renderRebuildResult(result) {
    var wrap = $("#rebuildResult");
    wrap.textContent = "";
    wrap.hidden = false;

    wrap.appendChild(
      text("div", "rebuild-summary" + (result.success ? "" : " err"), result.summary || (result.success ? "rebuild ran" : "rebuild failed"))
    );

    if (result.diffStat) {
      wrap.appendChild(text("pre", "rebuild-diffstat", result.diffStat));
    }

    if (result.changedFiles && result.changedFiles.length) {
      var list = el("ul", "rebuild-files");
      result.changedFiles.forEach(function (f) {
        list.appendChild(text("li", "", f));
      });
      wrap.appendChild(list);
    } else if (result.success) {
      wrap.appendChild(text("div", "hint", "No files changed."));
    }

    if (!result.success && result.stderr) {
      wrap.appendChild(text("div", "hint", "Subprocess stderr:"));
      wrap.appendChild(text("pre", "rebuild-stderr", result.stderr));
    }

    wrap.appendChild(
      text(
        "div",
        "hint",
        "Regenerated packs are NOT auto-committed — review the diff (git status / git diff) before committing."
      )
    );
  }

  // runRebuild POSTs to /api/knowledge/rebuild and shows a busy state while
  // the (synchronous, possibly multi-minute) subprocess runs — the button is
  // disabled and a status note appears; both clear once the response
  // arrives, whether the rebuild succeeded or failed. Always re-fetches
  // /api/config afterward so tokenEstimate reflects the newly-written pack.
  function runRebuild() {
    var btn = $("#rebuildBtn");
    var status = $("#rebuildStatus");
    var skipDistill = $("#rebuildSkipDistill").checked;
    btn.disabled = true;
    btn.classList.add("busy");
    status.textContent = "Rebuilding… this can take a few minutes.";
    $("#rebuildResult").hidden = true;

    apiFetch("POST", "/api/knowledge/rebuild", { skipDistill: skipDistill }).then(function (result) {
      btn.disabled = false;
      btn.classList.remove("busy");
      status.textContent = "";
      if (!result.ok) {
        showToast(apiErrorMessage(result, "Rebuild failed to start."), true);
        return;
      }
      var body = result.json || {};
      renderRebuildResult(body);
      showToast(body.summary || (body.success ? "Rebuild complete." : "Rebuild failed — review stderr."), !body.success);
      load();
    });
  }

  /* ---------------- keys ---------------- */

  // secretShortName strips the SSM param path down to its trailing segment
  // for the row's headline (e.g. "/kmv/secrets/use1/telephony/access_pin" ->
  // "access_pin") — the full path is still shown in the small subline below
  // it, so nothing is hidden, just de-emphasized.
  function secretShortName(name) {
    var parts = (name || "").split("/");
    return parts[parts.length - 1] || name;
  }

  var MASKED_VALUE = "•••••••••• value hidden";

  function renderKeys(cfg) {
    var list = $("#keyList");
    list.textContent = "";
    var secrets = cfg.secrets || [];

    if (!secrets.length) {
      list.appendChild(
        text("div", "hint", "No secret references imported from the live config.")
      );
      return;
    }

    secrets.forEach(function (s) {
      var row = el("div", "keyrow");

      var kname = el("div", "kname");
      kname.appendChild(document.createTextNode(secretShortName(s.name)));
      kname.appendChild(text("small", "", s.name + " · " + (s.mode || "unknown") + " mode"));
      row.appendChild(kname);

      var valSpan = text("span", "keyval", MASKED_VALUE);
      row.appendChild(valSpan);

      var ephemeralNote = text("span", "ephemeral-note", "ephemeral — not stored");
      ephemeralNote.hidden = true;
      row.appendChild(ephemeralNote);

      var kact = el("div", "kact");
      var revealBtn = el("button", "btn ghost");
      revealBtn.type = "button";
      revealBtn.textContent = "Reveal";
      revealBtn.setAttribute("aria-pressed", "false");
      kact.appendChild(revealBtn);
      kact.appendChild(text("span", "lockpill", "⚿ " + (s.store || "ssm")));
      var rotateBtn = el("button", "btn ghost");
      rotateBtn.type = "button";
      rotateBtn.appendChild(text("span", "k", "↻"));
      rotateBtn.appendChild(document.createTextNode(" Rotate"));
      kact.appendChild(rotateBtn);
      row.appendChild(kact);

      var rotateForm = el("div", "rotate-form");
      rotateForm.hidden = true;
      var newValInput = el("input", "input");
      newValInput.type = "text";
      newValInput.placeholder = "new value";
      newValInput.setAttribute("autocomplete", "off");
      var confirmBtn = el("button", "btn primary");
      confirmBtn.type = "button";
      confirmBtn.textContent = "Confirm rotate";
      var rotateCancelBtn = el("button", "btn ghost");
      rotateCancelBtn.type = "button";
      rotateCancelBtn.textContent = "Cancel";
      rotateForm.appendChild(newValInput);
      rotateForm.appendChild(confirmBtn);
      rotateForm.appendChild(rotateCancelBtn);
      row.appendChild(rotateForm);

      // ---- reveal/hide (SEC-01) ----
      // `revealed` is a plain closure-local boolean; the decrypted value
      // itself lives ONLY as valSpan.textContent for as long as `revealed`
      // is true. Nothing here ever touches localStorage, sessionStorage, a
      // cookie, or state.config — hiding (or navigating away, or any fresh
      // load()/render() call, which rebuilds this whole row from scratch)
      // clears it for good.
      var revealed = false;
      revealBtn.addEventListener("click", function () {
        if (revealed) {
          valSpan.textContent = MASKED_VALUE;
          valSpan.classList.remove("revealed");
          ephemeralNote.hidden = true;
          revealBtn.textContent = "Reveal";
          revealBtn.setAttribute("aria-pressed", "false");
          revealed = false;
          return;
        }
        revealBtn.disabled = true;
        apiFetch("POST", "/api/secret/reveal", { name: s.name }).then(function (result) {
          revealBtn.disabled = false;
          if (!result.ok) {
            showToast(apiErrorMessage(result, "Could not reveal this secret."), true);
            return;
          }
          var body = result.json || {};
          if (body.status !== "set") {
            showToast(secretShortName(s.name) + ": " + (body.status || "unavailable"), true);
            return;
          }
          valSpan.textContent = body.value;
          valSpan.classList.add("revealed");
          ephemeralNote.hidden = false;
          revealBtn.textContent = "Hide";
          revealBtn.setAttribute("aria-pressed", "true");
          revealed = true;
        });
      });

      // ---- rotate (SEC-02) ----
      rotateBtn.addEventListener("click", function () {
        rotateForm.hidden = !rotateForm.hidden;
        if (!rotateForm.hidden) newValInput.focus();
      });
      rotateCancelBtn.addEventListener("click", function () {
        rotateForm.hidden = true;
        newValInput.value = "";
      });
      confirmBtn.addEventListener("click", function () {
        var newValue = newValInput.value;
        if (!newValue) {
          showToast("Enter a new value before rotating.", true);
          return;
        }
        confirmBtn.disabled = true;
        apiFetch("POST", "/api/secret/rotate", { name: s.name, newValue: newValue }).then(function (result) {
          confirmBtn.disabled = false;
          if (!result.ok) {
            showToast(apiErrorMessage(result, "Could not rotate this secret."), true);
            return;
          }
          // The new value is never echoed back or displayed — success is
          // the only signal (SecretRotateResp carries no value field).
          newValInput.value = "";
          rotateForm.hidden = true;
          showToast(secretShortName(s.name) + " rotated.");
        });
      });

      list.appendChild(row);
    });
  }

  // renderProviderKeys renders PROVIDER_KEYS' static, inert rows: a
  // "SOPS-managed" pill and NO reveal/rotate buttons at all (T-17-11 — the
  // UI itself exposes no path to these three names, on top of the server's
  // own allow-list boundary in secret_reveal.go/secret_rotate.go).
  function renderProviderKeys() {
    var list = $("#providerKeyList");
    if (!list) return;
    list.textContent = "";
    PROVIDER_KEYS.forEach(function (k) {
      var row = el("div", "keyrow readonly");
      var kname = el("div", "kname");
      kname.appendChild(document.createTextNode(k.name));
      kname.appendChild(text("small", "", k.note));
      row.appendChild(kname);
      row.appendChild(text("span", "keyval", "sealed"));
      var kact = el("div", "kact");
      kact.appendChild(text("span", "lockpill sops", "⚿ SOPS-managed"));
      row.appendChild(kact);
      list.appendChild(row);
    });
  }

  /* ---------------- Save & deploy tab (SOP-01/02/03) ---------------- */

  // DEPLOY_STEPS mirrors the mockup's fixed "On deploy" ordered list — the
  // exact four steps the Plan-06 Deploy orchestrator runs in sequence. This
  // file never computes progress itself; deployStepStatuses below derives
  // each step's done/fail/skip/pending state purely from the DeployResult
  // JSON the /api/sop/deploy response carries.
  var DEPLOY_STEPS = [
    { id: "validate", title: "Validate SOP", desc: "Schema, secret refs resolve, no orphan packs." },
    { id: "write", title: "Write DynamoDB", desc: "Codes, tiers, phone mappings + repo config via kv key templates." },
    { id: "commit", title: "Commit YAML / TOML", desc: "manifest, topic-map, gate config — committed locally, never pushed." },
    { id: "refresh", title: "Refresh knowledge", desc: "refresh_knowledge.py, only if a pack source changed." }
  ];

  // APPLY_SURFACES / COMMIT_SURFACES classify a DeployResult.failedSurface
  // value into which of the four steps above it belongs to, mirroring
  // sop_deploy.go's own step ordering (Apply's five surfaces, then the two
  // direct gate/order writes + the scoped commit, then the conditional
  // knowledge rebuild).
  var APPLY_SURFACES = { rule: true, tier: true, did: true, unlock: true, knowledge: true };
  var COMMIT_SURFACES = { gate: true, order: true, "config-commit": true };

  // deployStepStatuses derives each of the four steps' render state from one
  // DeployResult: "fail" for the step that stopped Deploy short, "done" for
  // every step before it, "pending" (never reached) for every step after it.
  // A validation failure means every step past "validate" is untouched
  // (P-06-validate-first) — result itself may be null before any deploy has
  // run this session, in which case every step is "pending".
  function deployStepStatuses(result) {
    var statuses = { validate: "pending", write: "pending", commit: "pending", refresh: "pending" };
    if (!result) return statuses;

    if (result.validationErrors && result.validationErrors.length) {
      statuses.validate = "fail";
      return statuses;
    }
    statuses.validate = "done";

    var failedSurface = result.failedSurface || "";
    if (failedSurface && APPLY_SURFACES[failedSurface]) {
      statuses.write = "fail";
      return statuses;
    }
    statuses.write = "done";

    if (failedSurface && COMMIT_SURFACES[failedSurface]) {
      statuses.commit = "fail";
      return statuses;
    }
    statuses.commit = "done";

    if (failedSurface === "knowledge-rebuild") {
      statuses.refresh = "fail";
      return statuses;
    }
    statuses.refresh = result.refreshTriggered ? "done" : "skip";
    return statuses;
  }

  function stepNumGlyph(status, i) {
    if (status === "done") return "✓";
    if (status === "fail") return "✕";
    if (status === "skip") return "–";
    return String(i + 1);
  }

  // renderDeploySteps(null) renders the default all-pending list (tab open,
  // or after the name input changes and the prior result no longer applies).
  function renderDeploySteps(result) {
    var wrap = $("#sopSteps");
    if (!wrap) return;
    wrap.textContent = "";
    var statuses = deployStepStatuses(result);
    DEPLOY_STEPS.forEach(function (s, i) {
      var status = statuses[s.id];
      var stepEl = el("div", "step " + status);
      stepEl.appendChild(text("div", "num", stepNumGlyph(status, i)));
      var body = el("div", "st-b");
      body.appendChild(text("h4", "", s.title));
      body.appendChild(text("p", "", s.desc));
      stepEl.appendChild(body);
      wrap.appendChild(stepEl);
    });
  }

  // isDestructiveChangesetEntry flags the two changeset shapes T-18-22 calls
  // out as needing prominent marking: a "removed" entry (present live, not
  // in the named SOP — Apply never auto-removes it, but the operator must
  // still SEE the drift) and a rule's grant being repointed at the
  // zero-limit block tier (the same shape RULE-04's block uses).
  function isDestructiveChangesetEntry(e) {
    if (e.kind === "removed") return true;
    if (e.kind === "changed" && e.field === "tierId" && e.to === BLOCK_TIER_ID) return true;
    return false;
  }

  var CHANGESET_SURFACE_LABELS = {
    rule: "Rule",
    tier: "Tier",
    did: "DID",
    unlock: "Unlock",
    knowledge: "Knowledge",
    gate: "Gate",
    order: "Order"
  };

  function fmtChangesetValue(v) {
    if (v === null || v === undefined || v === "") return "—";
    if (Array.isArray(v)) return v.length ? v.join(", ") : "(empty)";
    if (typeof v === "object") return JSON.stringify(v);
    return String(v);
  }

  function changesetPathText(e) {
    var parts = [CHANGESET_SURFACE_LABELS[e.surface] || e.surface, e.key];
    if (e.field) parts.push(e.field);
    return parts.join(" · ");
  }

  function changesetWhatText(e) {
    if (e.kind === "added") return "new";
    if (e.kind === "removed") return "present live, not in SOP — never auto-removed";
    if (e.field) return fmtChangesetValue(e.from) + " → " + fmtChangesetValue(e.to);
    return "changed";
  }

  function renderChangesetPlaceholder(msg, lampKind) {
    var wrap = $("#sopChangeset");
    if (!wrap) return;
    wrap.textContent = "";
    var head = el("div", "ch-head");
    head.appendChild(lampEl(lampKind || "gated", "no changeset loaded"));
    wrap.appendChild(head);
    wrap.appendChild(text("div", "hint diff-empty", msg));
  }

  // renderChangeset renders one loaded []ChangesetEntry (SOP-02) grouped in
  // the server's own fixed surface order — added/changed/removed are
  // visually distinguished by line class, and any destructive entry
  // (isDestructiveChangesetEntry) carries an explicit "destructive" tag so
  // it can never be missed before Deploy (T-18-22).
  function renderChangeset(entries, name) {
    var wrap = $("#sopChangeset");
    if (!wrap) return;
    wrap.textContent = "";

    var destructiveCount = entries.filter(isDestructiveChangesetEntry).length;
    var head = el("div", "ch-head");
    var lampKind = !entries.length ? "live" : destructiveCount ? "block" : "gated";
    head.appendChild(lampEl(lampKind, entries.length + " change" + (entries.length === 1 ? "" : "s")));
    head.appendChild(text("span", "hint", "vs “" + name + "”"));
    wrap.appendChild(head);

    var diff = el("div", "diff");
    if (!entries.length) {
      diff.appendChild(text("div", "hint diff-empty", "No differences — live config already matches “" + name + "”."));
    } else {
      entries.forEach(function (e) {
        var destructive = isDestructiveChangesetEntry(e);
        var lineClass = e.kind === "added" ? "add" : e.kind === "removed" ? "rm" : "mod";
        var line = el("div", "line " + lineClass + (destructive ? " destructive" : ""));
        var sign = e.kind === "added" ? "+" : e.kind === "removed" ? "−" : "~";
        line.appendChild(text("span", "sign", sign));
        line.appendChild(text("span", "path", changesetPathText(e)));
        line.appendChild(text("span", "what", changesetWhatText(e)));
        if (destructive) line.appendChild(text("span", "destructive-tag", "destructive"));
        diff.appendChild(line);
      });
    }
    wrap.appendChild(diff);
  }

  // updateDeployGate is the UI-side gate the plan requires: Deploy stays
  // disabled until a changeset has been successfully loaded for EXACTLY the
  // name currently typed — so the operator always reviews the diff for the
  // SOP they're about to deploy, never a stale one from a previous name.
  function updateDeployGate() {
    var btn = $("#sopDeployBtn");
    var input = $("#sopNameInput");
    if (!btn || !input) return;
    var name = input.value.trim();
    var ready = !!(name && state.sop.changeset && state.sop.changesetName === name);
    btn.disabled = !ready;
  }

  function loadSopChangeset() {
    var input = $("#sopNameInput");
    var name = input ? input.value.trim() : "";
    if (!name) {
      showToast("Enter a SOP name first.", true);
      return;
    }
    renderChangesetPlaceholder("Loading changeset for “" + name + "”…", "gated");
    apiFetch("POST", "/api/sop/changeset", { name: name }).then(function (result) {
      if (!result.ok) {
        state.sop.changeset = null;
        state.sop.changesetName = null;
        renderChangesetPlaceholder(apiErrorMessage(result, "Could not load the changeset for “" + name + "”."), "block");
        updateDeployGate();
        return;
      }
      var entries = result.json || [];
      state.sop.changeset = entries;
      state.sop.changesetName = name;
      renderChangeset(entries, name);
      updateDeployGate();
    });
  }

  function renderSopSaveResult(body) {
    var wrap = $("#sopSaveResult");
    if (!wrap) return;
    wrap.textContent = "";
    wrap.hidden = false;
    var line = body.sha
      ? "Saved “" + body.name + "” as " + body.sha.slice(0, 10) + " — committed, ready to push/PR (not pushed automatically)."
      : "Saved “" + body.name + "” — no commit created (nothing changed since the last save).";
    wrap.appendChild(text("div", "sop-banner", line));
  }

  function runSopSave() {
    var input = $("#sopNameInput");
    var name = input ? input.value.trim() : "";
    if (!name) {
      showToast("Enter a SOP name first.", true);
      return;
    }
    var btn = $("#sopSaveBtn");
    btn.disabled = true;
    apiFetch("POST", "/api/sop/save", { name: name }).then(function (result) {
      btn.disabled = false;
      if (!result.ok) {
        showToast(apiErrorMessage(result, "Could not save this SOP."), true);
        return;
      }
      var body = result.json || {};
      renderSopSaveResult(body);
      showToast("Saved “" + name + "”.");
      // A fresh save changes what's ON DISK for this name, so any
      // previously loaded changeset (and the Deploy gate it satisfied) is
      // stale — reload it rather than leave a now-inaccurate diff on
      // screen.
      state.sop.changeset = null;
      state.sop.changesetName = null;
      loadSopChangeset();
    });
  }

  // renderDeployResult renders one full DeployResult (SOP-03): a validation
  // failure gets its own unmistakable banner listing every ValidationError
  // (ValidationError has no json tags — server.go/sop_validate.go serialize
  // it as {"ID":..., "Message":...}, so this reads those exact keys) and
  // stops there, since Deploy attempted nothing else (P-06-validate-first).
  // Otherwise it summarizes what Apply/commit/refresh actually did, always
  // stating explicitly that a commit is local-only ("ready to push/PR") and
  // that a knowledge refresh never auto-commits the regenerated packs.
  function renderDeployResult(result) {
    var wrap = $("#sopDeployResult");
    if (!wrap) return;
    wrap.textContent = "";
    wrap.hidden = false;

    var validationErrors = result.validationErrors || [];
    if (validationErrors.length) {
      var banner = el("div", "sop-banner err");
      banner.appendChild(
        text("div", "sop-banner-head", "Deploy refused — validation failed. Nothing was applied or committed.")
      );
      var list = el("ul", "sop-errlist");
      validationErrors.forEach(function (e) {
        var li = el("li");
        li.appendChild(text("span", "mono", (e.ID || e.id || "error") + ": "));
        li.appendChild(document.createTextNode(e.Message || e.message || ""));
        list.appendChild(li);
      });
      banner.appendChild(list);
      wrap.appendChild(banner);
      return;
    }

    var ok = !result.error;
    var summary = el("div", "sop-banner" + (ok ? "" : " err"));

    var applied = result.applied || [];
    var skipped = result.skipped || [];
    var appliedLine =
      applied.length + " change" + (applied.length === 1 ? "" : "s") + " applied" +
      (skipped.length ? ", " + skipped.length + " skipped (present live only — never auto-removed)." : ".");
    summary.appendChild(text("p", "", appliedLine));

    summary.appendChild(
      text(
        "p",
        "",
        result.commitSha
          ? "Committed locally as " + result.commitSha.slice(0, 10) + " — committed, ready to push/PR (not pushed automatically)."
          : "No repo-tracked config file changed — nothing to commit."
      )
    );

    if (result.refreshTriggered) {
      var rr = result.refreshResult;
      summary.appendChild(
        text(
          "p",
          "",
          "Knowledge refresh triggered" + (rr && rr.summary ? ": " + rr.summary : "") +
            ". Regenerated packs are NOT auto-committed — review the diff yourself."
        )
      );
    } else {
      summary.appendChild(text("p", "", "No knowledge pack source changed — refresh skipped."));
    }

    if (result.error) {
      summary.appendChild(
        text("p", "sop-banner-head", "Failed at “" + (result.failedSurface || "?") + "”: " + result.error)
      );
    }

    wrap.appendChild(summary);
  }

  function runSopDeploy() {
    var input = $("#sopNameInput");
    var name = input ? input.value.trim() : "";
    var btn = $("#sopDeployBtn");
    if (!name || !btn || btn.disabled) return; // UI gate: review the changeset first
    if (!window.confirm("Deploy “" + name + "” now? This applies the changeset live and commits config files locally.")) return;

    btn.disabled = true;
    btn.classList.add("busy");
    apiFetch("POST", "/api/sop/deploy", { name: name }).then(function (result) {
      btn.classList.remove("busy");
      var body = result.json || {};
      renderDeploySteps(body);
      renderDeployResult(body);

      if ((body.validationErrors || []).length) {
        showToast("Deploy refused — validation failed.", true);
      } else if (!result.ok || body.error) {
        showToast(apiErrorMessage(result, "Deploy failed at “" + (body.failedSurface || "?") + "”."), true);
      } else {
        showToast("Deployed “" + name + "”.");
      }

      // Whatever happened, the changeset just reviewed is now stale (Deploy
      // may have changed live state) — require a fresh load before the next
      // Deploy click, and refresh every other tab's data since Apply may
      // have written DynamoDB/repo config this render() depends on.
      state.sop.changeset = null;
      state.sop.changesetName = null;
      updateDeployGate();
      loadSopChangeset();
      load();
    });
  }

  /* ---------------- nav counts + meta stamp ---------------- */

  function updateNavCounts(cfg) {
    $("#navCountRules").textContent = String((cfg.rules || []).length);
    $("#navCountKnowledge").textContent = String((cfg.knowledge || []).length);
    $("#navCountKeys").textContent = String((cfg.secrets || []).length);
  }

  function updateMetaStamp(cfg) {
    var meta = cfg.meta || {};
    var when = meta.importedAtMs ? new Date(meta.importedAtMs).toLocaleString() : "unknown time";
    $("#metaStamp").textContent =
      (meta.generator || "kv studio") +
      " · " +
      (meta.region || "?") +
      " · " +
      (meta.profile || "?") +
      " · imported " +
      when;
  }

  /* ---------------- top-level render ---------------- */

  function render(cfg) {
    state.config = cfg;

    if (cfg && cfg.error) {
      showError(cfg.error.message, cfg.error.region, cfg.error.profile);
      return;
    }

    hideError();
    renderSummary(cfg);
    renderRules(cfg);
    renderDids(cfg);
    renderPacks(cfg);
    renderKeys(cfg);
    renderProviderKeys();
    updateNavCounts(cfg);
    updateMetaStamp(cfg);
  }

  /* ---------------- tabs ---------------- */

  function switchTab(tab) {
    var meta = TAB_META[tab];
    if (!meta) return;
    $all(".navitem").forEach(function (n) {
      n.classList.toggle("active", n.dataset.tab === tab);
    });
    $all(".panel").forEach(function (p) {
      p.classList.toggle("active", p.id === "panel-" + tab);
    });
    $("#tabTitle").textContent = meta.t;
    $("#tabDesc").textContent = meta.d;
    var newRuleBtn = $("#newRuleBtn");
    if (newRuleBtn) newRuleBtn.hidden = tab !== "rules";

    // Fetch the changeset FRESH on every Save & deploy tab open — reflects
    // any live edit made via another tab since the last time this tab was
    // open (key_links: "never cached"). A blank name just leaves the
    // placeholder prompt in place; nothing to fetch yet.
    if (tab === "deploy") {
      var nameInput = $("#sopNameInput");
      if (nameInput && nameInput.value.trim()) {
        loadSopChangeset();
      }
      updateDeployGate();
    }
  }

  /* ---------------- data load ---------------- */

  function load() {
    hideError();
    fetch("/api/config")
      .then(function (resp) {
        if (!resp.ok) {
          return resp
            .json()
            .catch(function () {
              return null;
            })
            .then(function (body) {
              var err = body && body.error;
              showError(
                (err && err.message) ||
                  "kv studio received HTTP " + resp.status + " from /api/config.",
                err && err.region,
                err && err.profile
              );
              throw new Error("config fetch failed: " + resp.status);
            });
        }
        return resp.json();
      })
      .then(function (cfg) {
        render(cfg);
      })
      .catch(function (err) {
        if (!$("#errorBanner").hidden) return; // already shown above
        showError("kv studio could not reach /api/config: " + err.message);
      });
  }

  /* ---------------- init ---------------- */

  document.addEventListener("DOMContentLoaded", function () {
    var nav = $("#nav");
    if (nav) {
      nav.addEventListener("click", function (e) {
        var btn = e.target.closest(".navitem");
        if (btn) switchTab(btn.dataset.tab);
      });
    }
    var reloadBtn = $("#reloadBtn");
    if (reloadBtn) reloadBtn.addEventListener("click", load);

    // ---- rule-editor drawer wiring ----
    var newRuleBtn = $("#newRuleBtn");
    if (newRuleBtn) newRuleBtn.addEventListener("click", function () { openRuleDrawer(null); });
    var drawerClose = $("#drawerClose");
    if (drawerClose) drawerClose.addEventListener("click", closeRuleDrawer);
    var drawerCancel = $("#drawerCancel");
    if (drawerCancel) drawerCancel.addEventListener("click", closeRuleDrawer);
    var scrim = $("#scrim");
    if (scrim) scrim.addEventListener("click", closeRuleDrawer);
    var drawerSave = $("#drawerSave");
    if (drawerSave) drawerSave.addEventListener("click", saveRuleDrawer);
    var drawerDelete = $("#drawerDelete");
    if (drawerDelete) drawerDelete.addEventListener("click", deleteRuleDrawer);
    var drawerBlock = $("#drawerBlock");
    if (drawerBlock) drawerBlock.addEventListener("click", blockRuleDrawer);

    // ---- DID manager modal wiring ----
    var manageDidsBtn = $("#manageDidsBtn");
    if (manageDidsBtn) manageDidsBtn.addEventListener("click", openDidModal);
    var didModalClose = $("#didModalClose");
    if (didModalClose) didModalClose.addEventListener("click", closeDidModal);
    var didScrim = $("#didScrim");
    if (didScrim) didScrim.addEventListener("click", closeDidModal);
    var didSearch = $("#didSearch");
    if (didSearch) {
      didSearch.addEventListener("input", function (e) {
        renderDidModalList(e.target.value);
      });
    }
    var didAddBtn = $("#didAddBtn");
    if (didAddBtn) didAddBtn.addEventListener("click", function () { showDidForm("add", null); });

    // ---- knowledge rebuild wiring (KNOW-03) ----
    var rebuildBtn = $("#rebuildBtn");
    if (rebuildBtn) rebuildBtn.addEventListener("click", runRebuild);

    // ---- Save & deploy tab wiring (SOP-01/02/03) ----
    renderDeploySteps(null);
    var sopNameInput = $("#sopNameInput");
    if (sopNameInput) {
      renderChangesetPlaceholder("Enter a SOP name above, then Load changeset.", "gated");
      sopNameInput.addEventListener("input", function () {
        // The name no longer matches whatever changeset (if any) was last
        // loaded — re-gate immediately rather than let a stale changeset
        // for a DIFFERENT name keep Deploy enabled.
        updateDeployGate();
      });
      sopNameInput.addEventListener("keydown", function (e) {
        if (e.key === "Enter") {
          e.preventDefault();
          loadSopChangeset();
        }
      });
    }
    var sopChangesetBtn = $("#sopChangesetBtn");
    if (sopChangesetBtn) sopChangesetBtn.addEventListener("click", loadSopChangeset);
    var sopSaveBtn = $("#sopSaveBtn");
    if (sopSaveBtn) sopSaveBtn.addEventListener("click", runSopSave);
    var sopDeployBtn = $("#sopDeployBtn");
    if (sopDeployBtn) sopDeployBtn.addEventListener("click", runSopDeploy);

    // Escape closes whichever overlay is currently open — never both, and
    // never interferes with normal in-page typing.
    document.addEventListener("keydown", function (e) {
      if (e.key !== "Escape") return;
      var drawerEl = $("#drawer");
      var didModalEl = $("#didModal");
      if (drawerEl && !drawerEl.hidden) closeRuleDrawer();
      else if (didModalEl && !didModalEl.hidden) closeDidModal();
    });

    load();
  });
})();
