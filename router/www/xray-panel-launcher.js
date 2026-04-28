// xray-panel-launcher.js — injects a launch link into the GL.iNet stock
// admin UI for the xray server panel at :9092 on a Flint 2.
//
// One entry: appended INSIDE the expanded VPN sidebar submenu as the
// last item (after Tor / WireGuard server / OpenVPN / etc), styled like
// a native sub-row. Cloned from an existing submenu item so indent /
// icon-slot / font / padding all match exactly. Click opens the panel
// in a new tab.
//
// No fallback / overlay: if the anchor can't be found we retry until
// it appears (the GL.iNet UI is a Vue SPA — anchors land milliseconds
// after DOMContentLoaded). If retries time out, the user falls back
// to opening :9092 directly. That's strictly better than overlay
// garbage.
//
// To remove the integration entirely:
//   ssh flint2 'cp /www/gl_home.html.bak /www/gl_home.html; rm -f /www/xray-panel-launcher.js'
(function () {
    "use strict";

    var PORT = 9092;
    var LABEL = "XRAY server";
    var SIDEBAR_ID = "xray-panel-sidebar";
    var MAX_TRIES  = 60;       // 60 × 250ms = 15s, plenty for SPA boot
    var POLL_MS    = 250;
    // Status-dot poll cadence. The xray service flips state rarely
    // (manual start/stop from our panel), so 5s is plenty fresh
    // without spamming the router.
    var STATUS_POLL_MS = 5000;
    var STATUS_DOT_CLASS = "xray-status-dot";
    var STATUS_DOT_CSS_ID = "xray-status-dot-style";

    function panelURL() {
        return location.protocol.replace("https:", "http:") +
               "//" + location.hostname + ":" + PORT + "/";
    }

    function panelOriginNoSlash() {
        return location.protocol.replace("https:", "http:") +
               "//" + location.hostname + ":" + PORT;
    }

    // probeUp loads /api/up.png as an <img>. 200 → onload (server up),
    // 404 → onerror (server down). No CORS preflight, no credentials,
    // no auth dialog. 4s hard timeout falls back to "down".
    function probeUp(onResult) {
        var done = false;
        var img = new Image();
        function finish(ok) {
            if (done) return;
            done = true;
            try { onResult(ok); } catch (e) {}
        }
        img.onload  = function () { finish(true); };
        img.onerror = function () { finish(false); };
        img.src = panelOriginNoSlash() + "/api/up.png?ts=" + Date.now();
        setTimeout(function () { finish(false); }, 4000);
    }

    // GL.iNet renders its native .status-badge conditionally — when a
    // sidebar item's service is inactive the badge is a Vue comment
    // placeholder, not an element, so cloneNode picks up nothing to
    // toggle. Inject our own dot styled to match the native size /
    // shape / position, coloured via the theme's --text-menu-active
    // variable so it tracks light / dark switching automatically.
    function injectStatusDotCSS() {
        if (document.getElementById(STATUS_DOT_CSS_ID)) return;
        var style = document.createElement("style");
        style.id = STATUS_DOT_CSS_ID;
        // Pick up the same colour the native .status-badge.is-active
        // uses (--text-menu-active) so our dot matches stock dots
        // exactly across themes / firmware versions. Fallback to the
        // observed gl-teal #1ec3a4 if the variable isn't defined for
        // some reason.
        style.textContent =
            "." + STATUS_DOT_CLASS + " { " +
                "display: inline-block !important; " +
                "width: 8px; height: 8px; " +
                "border-radius: 50%; " +
                "margin-left: 10px; " +
                "vertical-align: middle; " +
                "flex-shrink: 0; " +
                "background-color: transparent; " +
            "} " +
            "." + STATUS_DOT_CLASS + ".is-active { " +
                "background-color: var(--text-menu-active, #1ec3a4) !important; " +
            "}";
        document.head.appendChild(style);
    }

    function ensureStatusDot(entry) {
        var dot = entry.querySelector("." + STATUS_DOT_CLASS);
        if (dot) return dot;
        dot = document.createElement("span");
        dot.className = STATUS_DOT_CLASS;
        // Place the dot right after the visible label so it sits
        // where the native .status-badge would sit on stock items.
        var label = entry.querySelector(".menu-title") ||
                    entry.querySelector("span");
        if (label && label.parentNode) {
            label.parentNode.insertBefore(dot, label.nextSibling);
        } else {
            entry.appendChild(dot);
        }
        return dot;
    }

    function setStatusDot(active) {
        var entry = document.getElementById(SIDEBAR_ID);
        if (!entry) {
            try { console.log("[xray-panel] setStatusDot: entry missing"); } catch (e) {}
            return;
        }
        injectStatusDotCSS();
        var dot = ensureStatusDot(entry);
        if (active) {
            dot.classList.add("is-active");
        } else {
            dot.classList.remove("is-active");
        }
        try { console.log("[xray-panel] dot →", active ? "ACTIVE" : "inactive", dot); } catch (e) {}
    }

    function tickStatus() {
        if (!document.getElementById(SIDEBAR_ID)) return;
        probeUp(setStatusDot);
    }

    function startStatusPoll() {
        tickStatus();
        setInterval(tickStatus, STATUS_POLL_MS);
    }

    // Poll until check() returns truthy; then call onFound(value). Uses
    // setTimeout with a small interval so the page can paint between
    // attempts. Caps at MAX_TRIES to avoid runaway loops.
    function whenReady(check, onFound) {
        var tries = 0;
        function attempt() {
            try {
                var v = check();
                if (v) { onFound(v); return; }
            } catch (e) {}
            if (++tries < MAX_TRIES) setTimeout(attempt, POLL_MS);
        }
        attempt();
    }

    function ancestorLI(node) {
        for (var d = 0; d < 8 && node; d++) {
            if (node.tagName === "LI") return node;
            node = node.parentElement;
        }
        return null;
    }

    // ── sidebar — last item inside the VPN expanded submenu ─────────
    //
    // Strategy: find a leaf labelled with a known VPN submenu entry —
    // that text only appears once on the page in the right place.
    // Walk up to its containing <li>; that's our template. Cloning
    // an existing submenu <li> inherits the exact indent / icon-slot /
    // typography GL.iNet's stylesheet applies to native rows.
    //
    // When the user collapses the VPN section, our entry collapses
    // with it — the natural behaviour, since we live inside that <ul>.
    function findVPNSubmenuTemplate() {
        var rxKnown = /^(tor|wireguard server|wireguard client|openvpn server|openvpn client|vpn dashboard)$/i;
        var els = document.querySelectorAll("span, a, div, em, b");
        for (var i = 0; i < els.length; i++) {
            var el = els[i];
            if (el.children.length > 0) continue;
            var t = (el.textContent || "").trim();
            if (rxKnown.test(t)) {
                var li = ancestorLI(el);
                // It must live inside a <ul> with at least 2 sibling
                // submenu rows (otherwise we'd append to the wrong
                // place — e.g. a non-VPN solo menu).
                if (li && li.parentNode && li.parentNode.children.length >= 2) {
                    return li;
                }
            }
        }
        return null;
    }

    function renderSidebar() {
        if (document.getElementById(SIDEBAR_ID)) return;

        whenReady(findVPNSubmenuTemplate, function (template) {
            if (document.getElementById(SIDEBAR_ID)) return;
            if (!template || !template.parentNode) return;
            var sub = template.parentNode;
            var clone = template.cloneNode(true);
            clone.id = SIDEBAR_ID;

            // Strip any router-link wiring — Vue attaches navigation
            // via @click which doesn't survive cloneNode, but href on
            // child <a> tags does, so blank them.
            var hrefEls = clone.querySelectorAll("[href]");
            for (var h = 0; h < hrefEls.length; h++) {
                hrefEls[h].removeAttribute("href");
                hrefEls[h].removeAttribute("target");
            }
            var clickEls = clone.querySelectorAll("[onclick]");
            for (var c = 0; c < clickEls.length; c++) {
                clickEls[c].removeAttribute("onclick");
            }
            // Drop any "active" / "router-link-active" classes the
            // template happened to carry — our entry is never the
            // currently-routed page (it opens in a new tab).
            var activeEls = clone.querySelectorAll("[class*='active']");
            for (var a = 0; a < activeEls.length; a++) {
                activeEls[a].className = activeEls[a].className
                    .replace(/\b\S*active\S*\b/g, "")
                    .replace(/\s+/g, " ").trim();
            }
            if (typeof clone.className === "string") {
                clone.className = clone.className
                    .replace(/\b\S*active\S*\b/g, "")
                    .replace(/\s+/g, " ").trim();
            }

            // Replace the label leaf (whatever text the template had)
            // with our LABEL. Pick the deepest leaf with non-empty
            // text — that's the visible label.
            var leaves = clone.querySelectorAll("*");
            var labelLeaf = null;
            for (var i = 0; i < leaves.length; i++) {
                var l = leaves[i];
                if (l.children.length > 0) continue;
                var t = (l.textContent || "").trim();
                if (t.length > 0 && !/^[\s 　]+$/.test(t)) {
                    labelLeaf = l;
                }
            }
            if (labelLeaf) {
                labelLeaf.textContent = LABEL;
            } else {
                clone.textContent = LABEL;
            }

            // Wrap the clone's contents in an anchor that opens our
            // panel in a new tab, bypassing the SPA router.
            var url = panelURL();
            var wrapper = document.createElement("a");
            wrapper.href = url;
            wrapper.target = "_blank";
            wrapper.rel = "noopener";
            wrapper.title = LABEL + " (" + url + ")";
            wrapper.style.cssText = "display:block;color:inherit;text-decoration:none;cursor:pointer";
            while (clone.firstChild) wrapper.appendChild(clone.firstChild);
            clone.appendChild(wrapper);

            // Append at the end of the VPN submenu.
            sub.appendChild(clone);

            // Immediately probe + paint so the dot doesn't wait a
            // full 5s tick after the sidebar renders.
            try { tickStatus(); } catch (e) {}
        });
    }

    function init() {
        try { renderSidebar(); } catch (e) {}
        try { startStatusPoll(); } catch (e) {}
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }

    // The SPA may rerender the sidebar (e.g. on language switch); if
    // our entry gets blown away, re-insert. Debounced observer.
    if (typeof MutationObserver !== "undefined") {
        var pending = false;
        new MutationObserver(function () {
            if (pending) return;
            pending = true;
            setTimeout(function () {
                pending = false;
                if (!document.getElementById(SIDEBAR_ID)) renderSidebar();
                // SPA may also rerender our entry, dropping the
                // is-active class we toggle on its .status-badge.
                // Re-tick on the next frame so the dot stays in
                // sync without waiting a full poll interval.
                tickStatus();
            }, 250);
        }).observe(document.body, { childList: true, subtree: true });
    }
})();
