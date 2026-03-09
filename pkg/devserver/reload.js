// hamr dev — SSE-based live reload client.
// Injected into HTML responses by the dev proxy.

(function() {
    "use strict";

    var MIN_DELAY = 1000;
    var MAX_DELAY = 30000;
    var delay = MIN_DELAY;
    var source = null;

    // --- State ---

    var widget = null;
    var panel = null;
    var panelOpen = false;
    var config = null;        // {rules:[], daemons:[]}
    var buildingRules = {};   // name -> true while building
    var ruleErrors = {};      // name -> {output: "..."} for failed builds
    var connectionState = "disconnected";

    // --- Logs State ---

    var logLines = [];           // accumulated {rule, text} entries
    var logOverlay = null;       // overlay DOM element
    var LOG_VISIBLE = 50;        // visible lines (controls window height)
    var BUFFER_MAX = 5000;       // max stored lines (internal cap)
    var LOG_LINE_H = 16.5;       // line height in px (11px * 1.5)
    var STORAGE_KEY = "__hamr_logs";
    var STORAGE_KEY_CAP = "__hamr_logs_cap";

    // Restore saved visible lines.
    try {
        var savedCap = parseInt(localStorage.getItem(STORAGE_KEY_CAP), 10);
        if (savedCap > 0) LOG_VISIBLE = savedCap;
    } catch(e) {}

    // --- ANSI to HTML ---

    var ANSI_COLORS = {
        "30": "#4B5563", "31": "#F87171", "32": "#4ADE80", "33": "#FCD34D",
        "34": "#60A5FA", "35": "#C084FC", "36": "#67E8F9", "37": "#E8E8E8",
        "90": "#6B7280", "91": "#FCA5A5", "92": "#86EFAC", "93": "#FDE68A",
        "94": "#93C5FD", "95": "#D8B4FE", "96": "#A5F3FC", "97": "#FFFFFF"
    };

    function ansiToHtml(s) {
        var result = "";
        var open = false;
        var i = 0;
        while (i < s.length) {
            if (s.charCodeAt(i) === 0x1B && i + 1 < s.length && s[i + 1] === "[") {
                var j = i + 2;
                while (j < s.length && s[j] !== "m") j++;
                if (j < s.length) {
                    var codes = s.substring(i + 2, j).split(";");
                    if (open) { result += "</span>"; open = false; }
                    var styles = [];
                    for (var c = 0; c < codes.length; c++) {
                        var code = codes[c];
                        if (code === "1") {
                            styles.push("font-weight:bold");
                        } else if (ANSI_COLORS[code]) {
                            styles.push("color:" + ANSI_COLORS[code]);
                        }
                        // "0" or "" = reset, just close span (already done above)
                    }
                    if (styles.length > 0) {
                        result += '<span style="' + styles.join(";") + '">';
                        open = true;
                    }
                    i = j + 1;
                    continue;
                }
            }
            var ch = s[i];
            if (ch === "<") result += "&lt;";
            else if (ch === ">") result += "&gt;";
            else if (ch === "&") result += "&amp;";
            else if (ch === '"') result += "&quot;";
            else result += ch;
            i++;
        }
        if (open) result += "</span>";
        return result;
    }

    // --- Status Widget ---

    function createWidget() {
        var style = document.createElement("style");
        style.textContent =
            "@keyframes __hamr-pulse{0%,100%{opacity:1}50%{opacity:0.4}}" +
            "@keyframes __hamr-spin{0%{transform:rotate(0deg)}100%{transform:rotate(360deg)}}" +
            "#__hamr-status{position:fixed;bottom:16px;left:16px;z-index:99999;" +
            "width:60px;height:60px;border-radius:12px;border:3px solid #FFB347;" +
            "background:#1F2326;display:flex;align-items:center;justify-content:center;" +
            "cursor:pointer;opacity:0.6;transition:opacity 0.2s,border-color 0.3s;" +
            "box-shadow:0 2px 8px rgba(0,0,0,0.3);overflow:hidden;}" +
            "#__hamr-status:hover{opacity:1}" +
            "#__hamr-status.disconnected{border-color:#C92E0A;opacity:1;animation:__hamr-pulse 1.5s ease-in-out infinite}" +
            "#__hamr-status.error{border-color:#EF4444;opacity:1}" +
            "#__hamr-status.reloading{border-color:#FF8C32;opacity:1}" +
            "#__hamr-status.reloading img{animation:__hamr-spin 1s ease-out infinite}" +

            // Logs checkbox in panel
            "#__hamr-panel .hp-logs-row{display:flex;align-items:center;gap:8px;padding:10px 14px;" +
            "border-top:1px solid #2E3338;cursor:pointer;user-select:none;}" +
            "#__hamr-panel .hp-logs-row:hover{background:#2A2E33;}" +
            "#__hamr-panel .hp-logs-row input{accent-color:#FFB347;margin:0;cursor:pointer;}" +
            "#__hamr-panel .hp-logs-row span{font-size:12px;font-weight:600;color:#8B8B8B;}" +
            "#__hamr-panel .hp-logs-row:hover span{color:#E8E8E8;}" +

            // Panel styles — opens upward above the widget
            "#__hamr-panel{position:fixed;bottom:86px;left:16px;z-index:100001;" +
            "width:320px;max-height:calc(100vh - 102px);overflow-y:auto;background:#1F2326;border:1px solid #3A3F45;border-radius:10px;" +
            "box-shadow:0 4px 20px rgba(0,0,0,0.5);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;" +
            "font-size:13px;color:#D4D4D4;overflow:hidden;" +
            "transform:translateY(8px);opacity:0;transition:transform 0.15s ease-out,opacity 0.15s ease-out;pointer-events:none;}" +
            "#__hamr-panel.open{transform:translateY(0);opacity:1;pointer-events:auto;}" +
            "#__hamr-panel *{box-sizing:border-box;}" +

            // Header
            "#__hamr-panel .hp-header{display:flex;align-items:center;justify-content:space-between;" +
            "padding:12px 14px;border-bottom:1px solid #3A3F45;}" +
            "#__hamr-panel .hp-title{font-weight:600;font-size:14px;color:#E8E8E8;}" +
            "#__hamr-panel .hp-dot{width:8px;height:8px;border-radius:50%;flex-shrink:0;}" +
            "#__hamr-panel .hp-dot.connected{background:#4ADE80;}" +
            "#__hamr-panel .hp-dot.disconnected{background:#C92E0A;animation:__hamr-pulse 1.5s ease-in-out infinite;}" +

            // Section
            "#__hamr-panel .hp-section{padding:10px 14px;}" +
            "#__hamr-panel .hp-section+.hp-section{border-top:1px solid #2E3338;}" +
            "#__hamr-panel .hp-label{font-size:10px;text-transform:uppercase;letter-spacing:0.05em;" +
            "color:#8B8B8B;margin-bottom:8px;font-weight:600;}" +

            // Rule row
            "#__hamr-panel .hp-rule{display:flex;align-items:center;gap:8px;padding:4px 0;}" +
            "#__hamr-panel .hp-rule-dot{width:6px;height:6px;border-radius:50%;flex-shrink:0;background:#4B5563;}" +
            "#__hamr-panel .hp-rule-dot.building{background:#FF8C32;animation:__hamr-pulse 1s ease-in-out infinite;}" +
            "#__hamr-panel .hp-rule-name{font-weight:500;color:#E8E8E8;}" +
            "#__hamr-panel .hp-rule-detail{color:#6B7280;font-size:11px;margin-left:auto;white-space:nowrap;" +
            "overflow:hidden;text-overflow:ellipsis;max-width:160px;}" +

            // Daemon row
            "#__hamr-panel .hp-daemon{padding:4px 0;}" +
            "#__hamr-panel .hp-daemon-name{color:#E8E8E8;font-weight:500;}" +
            "#__hamr-panel .hp-daemon-cmd{color:#6B7280;font-size:11px;margin-top:1px;}" +

            // Error section
            "#__hamr-panel .hp-error-label{color:#EF4444;}" +
            "#__hamr-panel .hp-error-rule{font-weight:600;color:#E8E8E8;margin-bottom:4px;}" +
            "#__hamr-panel .hp-error-output{background:#18181B;border:1px solid #3A3F45;border-radius:6px;" +
            "padding:8px;font-family:'SF Mono',Monaco,Consolas,monospace;font-size:11px;color:#F87171;" +
            "max-height:200px;overflow-y:auto;white-space:pre-wrap;word-break:break-all;margin-bottom:8px;}" +

            // Error dot
            "#__hamr-panel .hp-rule-dot.error{background:#EF4444;}" +

            // Logs overlay — to the right of the widget, fills remaining width
            "#__hamr-logs{position:fixed;bottom:16px;left:86px;right:16px;z-index:100000;" +
            "background:#1A1D20;border:1px solid #3A3F45;border-radius:10px;" +
            "box-shadow:0 4px 24px rgba(0,0,0,0.6);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;" +
            "font-size:13px;color:#D4D4D4;overflow:hidden;}" +
            "#__hamr-logs *{box-sizing:border-box;}" +
            "#__hamr-logs .hl-header{display:flex;align-items:center;justify-content:space-between;" +
            "padding:10px 14px;border-bottom:1px solid #3A3F45;flex-shrink:0;}" +
            "#__hamr-logs .hl-title{font-weight:600;font-size:14px;color:#E8E8E8;}" +
            "#__hamr-logs .hl-close{background:none;border:none;color:#6B7280;font-size:18px;" +
            "cursor:pointer;padding:0 4px;line-height:1;}" +
            "#__hamr-logs .hl-close:hover{color:#E8E8E8;}" +
            "#__hamr-logs .hl-cap{display:flex;align-items:center;gap:0;margin-left:auto;margin-right:12px;" +
            "font-size:11px;color:#6B7280;background:#2A2E33;border:1px solid #3A3F45;border-radius:5px;" +
            "padding:3px 8px 3px 8px;}" +
            "#__hamr-logs .hl-cap span{margin-right:6px;}" +
            "#__hamr-logs .hl-cap input{width:48px;background:transparent;border:none;" +
            "color:#D4D4D4;font-size:11px;font-family:'SF Mono',Monaco,Consolas,monospace;text-align:right;padding:0;" +
            "-moz-appearance:textfield;}" +
            "#__hamr-logs .hl-cap input::-webkit-outer-spin-button," +
            "#__hamr-logs .hl-cap input::-webkit-inner-spin-button{-webkit-appearance:none;margin:0;}" +
            "#__hamr-logs .hl-cap input:focus{outline:none;color:#FFB347;}" +
            "#__hamr-logs .hl-body{overflow-y:auto;padding:8px 12px;" +
            "font-family:'SF Mono',Monaco,Consolas,monospace;font-size:11px;" +
            "line-height:1.5;white-space:pre-wrap;word-break:break-all;}" +
            "#__hamr-logs .hl-line{}" +
            "#__hamr-logs .hl-rule{color:#6B7280;}";
        document.head.appendChild(style);

        widget = document.createElement("div");
        widget.id = "__hamr-status";
        widget.title = "hamr: connecting...";
        widget.innerHTML = '<img src="/__hamr/logo.png" width="48" height="48" alt="hamr" style="display:block;pointer-events:none">';
        widget.addEventListener("click", function(e) {
            e.stopPropagation();
            togglePanel();
        });
        document.body.appendChild(widget);

        // Restore logs overlay from localStorage.
        try {
            if (localStorage.getItem(STORAGE_KEY) === "1") {
                openLogOverlay();
            }
        } catch(e) {}

        // Close panel on outside click.
        document.addEventListener("click", function(e) {
            if (panelOpen && panel && !panel.contains(e.target) && !widget.contains(e.target)) {
                closePanel();
            }
        });
    }

    function stripAnsi(s) {
        return s.replace(/\x1B\[[0-9;]*[A-Za-z]/g, "");
    }

    function hasErrors() {
        for (var k in ruleErrors) {
            if (ruleErrors.hasOwnProperty(k)) return true;
        }
        return false;
    }

    function updateWidgetState() {
        if (!widget) return;
        if (hasErrors()) {
            setWidgetClass("error", "hamr: build errors");
        } else {
            var anyBuilding = false;
            for (var k in buildingRules) {
                if (buildingRules.hasOwnProperty(k)) { anyBuilding = true; break; }
            }
            if (anyBuilding) {
                setWidgetClass("reloading", "hamr: building...");
            } else {
                setWidgetClass("", "hamr: connected");
            }
        }
        updatePanelStatus();
    }

    function setWidgetClass(cls, title) {
        if (!widget) return;
        widget.className = cls;
        widget.title = title;
    }

    function setState(state) {
        if (connectionState === state) return;
        connectionState = state;
        if (!widget) return;
        if (state === "connected") {
            updateWidgetState();
        } else if (state === "disconnected") {
            widget.className = "disconnected";
            widget.title = "hamr: disconnected";
        } else if (state === "reloading") {
            widget.className = "reloading";
            widget.title = "hamr: reloading...";
        }
        updatePanelStatus();
    }

    function ensureWidget() {
        if (!widget && document.body) {
            createWidget();
        }
    }

    // --- Popup Panel ---

    function togglePanel() {
        if (panelOpen) {
            closePanel();
        } else {
            openPanel();
        }
    }

    function openPanel() {
        if (!config) return;
        ensurePanel();
        renderPanel();
        panelOpen = true;
        // Trigger reflow then add class for animation.
        panel.offsetHeight; // eslint-disable-line no-unused-expressions
        panel.classList.add("open");
    }

    function closePanel() {
        panelOpen = false;
        if (panel) {
            panel.classList.remove("open");
        }
    }

    function ensurePanel() {
        if (panel) return;
        panel = document.createElement("div");
        panel.id = "__hamr-panel";
        document.body.appendChild(panel);
    }

    function renderPanel() {
        if (!panel || !config) return;
        var dotClass = (connectionState === "connected") ? "connected" : "disconnected";
        var html = '<div class="hp-header">' +
            '<span class="hp-title">hamr dev</span>' +
            '<span class="hp-dot ' + dotClass + '"></span>' +
            '</div>';

        // Watch rules section.
        if (config.rules && config.rules.length > 0) {
            html += '<div class="hp-section"><div class="hp-label">Watching</div>';
            for (var i = 0; i < config.rules.length; i++) {
                var r = config.rules[i];
                var isBuilding = buildingRules[r.name];
                var isError = ruleErrors[r.name];
                var dotCls = isError ? "error" : (isBuilding ? "building" : "");
                var detail = r.cmd || r.run || "";
                html += '<div class="hp-rule">' +
                    '<span class="hp-rule-dot ' + dotCls + '"></span>' +
                    '<span class="hp-rule-name">' + esc(r.name) + '</span>' +
                    '<span class="hp-rule-detail" title="' + esc(detail) + '">' + esc(detail) + '</span>' +
                    '</div>';
            }
            html += '</div>';
        }

        // Errors section.
        if (hasErrors()) {
            html += '<div class="hp-section"><div class="hp-label hp-error-label">Errors</div>';
            for (var ek in ruleErrors) {
                if (!ruleErrors.hasOwnProperty(ek)) continue;
                var errInfo = ruleErrors[ek];
                var cleaned = stripAnsi(errInfo.output || "");
                var lines = cleaned.split("\n");
                if (lines.length > 50) {
                    lines = lines.slice(lines.length - 50);
                }
                html += '<div class="hp-error-rule">' + esc(ek) + '</div>' +
                    '<pre class="hp-error-output">' + esc(lines.join("\n")) + '</pre>';
            }
            html += '</div>';
        }

        // Daemons section.
        if (config.daemons && config.daemons.length > 0) {
            html += '<div class="hp-section"><div class="hp-label">Daemons</div>';
            for (var j = 0; j < config.daemons.length; j++) {
                var d = config.daemons[j];
                html += '<div class="hp-daemon">' +
                    '<span class="hp-daemon-name">' + esc(d.name) + '</span>' +
                    '<div class="hp-daemon-cmd">' + esc(d.cmd) + '</div>' +
                    '</div>';
            }
            html += '</div>';
        }

        // Logs checkbox row.
        var checked = isLogOverlayOpen() ? " checked" : "";
        html += '<label class="hp-logs-row">' +
            '<input type="checkbox" id="__hamr-logscb"' + checked + '>' +
            '<span>Terminal Logs</span>' +
            '</label>';

        panel.innerHTML = html;

        // Wire up the logs checkbox after innerHTML.
        var cb = document.getElementById("__hamr-logscb");
        if (cb) {
            cb.addEventListener("change", function() {
                if (cb.checked) {
                    openLogOverlay();
                } else {
                    closeLogOverlay();
                }
                try { localStorage.setItem(STORAGE_KEY, cb.checked ? "1" : "0"); } catch(e) {}
            });
        }
    }

    function updatePanelStatus() {
        if (!panelOpen || !panel) return;
        renderPanel();
    }

    function esc(s) {
        var d = document.createElement("div");
        d.textContent = s;
        return d.innerHTML;
    }

    // --- Logs Overlay ---

    function isLogOverlayOpen() {
        return logOverlay !== null;
    }

    function openLogOverlay() {
        if (isLogOverlayOpen()) return;

        logOverlay = document.createElement("div");
        logOverlay.id = "__hamr-logs";
        logOverlay.innerHTML =
            '<div class="hl-header">' +
            '<span class="hl-title">Terminal Logs</span>' +
            '<div class="hl-cap"><span>Max lines</span><input type="number" min="10" max="10000" step="10" value="' + LOG_VISIBLE + '"></div>' +
            '<button class="hl-close">&times;</button>' +
            '</div>' +
            '<div class="hl-body"></div>';
        document.body.appendChild(logOverlay);

        // Set body height based on visible lines.
        var body = logOverlay.querySelector(".hl-body");
        body.style.height = (LOG_VISIBLE * LOG_LINE_H) + "px";

        var capInput = logOverlay.querySelector(".hl-cap input");
        capInput.addEventListener("change", function() {
            var val = parseInt(capInput.value, 10);
            if (val >= 10 && val <= 10000) {
                LOG_VISIBLE = val;
                try { localStorage.setItem(STORAGE_KEY_CAP, String(val)); } catch(e) {}
                // Resize the body.
                var b = logOverlay.querySelector(".hl-body");
                if (b) b.style.height = (LOG_VISIBLE * LOG_LINE_H) + "px";
            }
        });

        logOverlay.querySelector(".hl-close").addEventListener("click", function() {
            closeLogOverlay();
            try { localStorage.setItem(STORAGE_KEY, "0"); } catch(e) {}
            // Sync the panel checkbox if panel is open.
            var cb = document.getElementById("__hamr-logscb");
            if (cb) cb.checked = false;
        });

        // Fetch history then render.
        fetch("/__hamr/logs").then(function(resp) {
            return resp.json();
        }).then(function(lines) {
            logLines = lines;
            body.innerHTML = "";
            for (var i = 0; i < logLines.length; i++) {
                body.appendChild(renderLogLine(logLines[i]));
            }
            body.scrollTop = body.scrollHeight;
        }).catch(function() {
            body.innerHTML = "";
            for (var i = 0; i < logLines.length; i++) {
                body.appendChild(renderLogLine(logLines[i]));
            }
            body.scrollTop = body.scrollHeight;
        });
    }

    function closeLogOverlay() {
        if (logOverlay) {
            logOverlay.remove();
            logOverlay = null;
        }
    }

    function appendLogLine(entry) {
        if (!isLogOverlayOpen()) return;
        var body = logOverlay.querySelector(".hl-body");
        if (!body) return;
        var atBottom = (body.scrollHeight - body.scrollTop - body.clientHeight) < 40;
        body.appendChild(renderLogLine(entry));
        // Cap DOM nodes at buffer max.
        while (body.childNodes.length > BUFFER_MAX) {
            body.removeChild(body.firstChild);
        }
        if (atBottom) {
            body.scrollTop = body.scrollHeight;
        }
    }

    function renderLogLine(entry) {
        var line = document.createElement("div");
        line.className = "hl-line";
        var ruleStyle = "";
        if (entry.color) {
            var m = entry.color.match(/\u001b\[(\d+)m/);
            if (m && ANSI_COLORS[m[1]]) {
                ruleStyle = ' style="color:' + ANSI_COLORS[m[1]] + '"';
            }
        }
        line.innerHTML = '<span class="hl-rule"' + ruleStyle + '>[' + esc(entry.rule) + ']</span> ' + ansiToHtml(entry.text);
        return line;
    }

    // --- Error Page Update ---

    function updateErrorPage() {
        if (!window.__hamr_error_page) return;
        var container = document.getElementById("__hamr-errors");
        if (!container) return;
        var html = "";
        var keys = [];
        for (var k in ruleErrors) {
            if (ruleErrors.hasOwnProperty(k)) keys.push(k);
        }
        keys.sort();
        for (var i = 0; i < keys.length; i++) {
            var rule = keys[i];
            var output = stripAnsi(ruleErrors[rule].output || "");
            html += '<div class="card">' +
                '<div class="card-header">' + esc(rule) + '</div>' +
                '<div class="card-body"><pre>' + esc(output) + '</pre></div>' +
                '</div>';
        }
        container.innerHTML = html;
    }

    // --- SSE Connection ---

    function connect() {
        ensureWidget();
        setState("disconnected");

        source = new EventSource("/__hamr/reload");

        source.addEventListener("connected", function() {
            delay = MIN_DELAY;
            buildingRules = {};
            ruleErrors = {};
            setState("connected");
            console.log("[hamr] live reload connected");
        });

        source.addEventListener("config", function(e) {
            try {
                config = JSON.parse(e.data);
            } catch (err) {
                console.warn("[hamr] bad config payload", err);
            }
        });

        source.addEventListener("output", function(e) {
            try {
                var entry = JSON.parse(e.data);
                logLines.push(entry);
                if (logLines.length > BUFFER_MAX) {
                    logLines = logLines.slice(-BUFFER_MAX);
                }
                appendLogLine(entry);
            } catch (err) {
                // ignore bad payload
            }
        });

        source.addEventListener("building", function(e) {
            var ruleName = e.data;
            if (ruleName) {
                buildingRules[ruleName] = true;
                delete ruleErrors[ruleName];
            }
            setState("reloading");
            updatePanelStatus();
            console.log("[hamr] building " + (ruleName || "..."));
        });

        source.addEventListener("build_error", function(e) {
            try {
                var payload = JSON.parse(e.data);
                ruleErrors[payload.rule] = {output: payload.output};
                delete buildingRules[payload.rule];
                updateWidgetState();
                if (window.__hamr_error_page) {
                    updateErrorPage();
                } else {
                    location.reload();
                }
                console.warn("[hamr] build error: " + payload.rule);
            } catch (err) {
                console.warn("[hamr] bad build_error payload", err);
            }
        });

        source.addEventListener("build_ok", function(e) {
            var ruleName = e.data;
            if (ruleName) {
                delete ruleErrors[ruleName];
                delete buildingRules[ruleName];
            }
            updateWidgetState();
            if (window.__hamr_error_page) {
                if (!hasErrors()) {
                    location.reload();
                } else {
                    updateErrorPage();
                }
            }
        });

        source.addEventListener("reload", function(e) {
            var scope = e.data || "full";

            // Clear building and error state on reload.
            buildingRules = {};
            ruleErrors = {};

            if (scope === "css") {
                reloadCSS();
                setState("connected");
                updateWidgetState();
                return;
            }

            setState("reloading");

            // Full reload: try morphing with Idiomorph if available.
            if (typeof Idiomorph !== "undefined") {
                fetch(location.href).then(function(resp) {
                    return resp.text();
                }).then(function(html) {
                    Idiomorph.morph(document.documentElement, html);
                    setState("connected");
                    updatePanelStatus();
                    console.log("[hamr] page morphed");
                }).catch(function() {
                    location.reload();
                });
            } else {
                location.reload();
            }
        });

        source.onerror = function() {
            source.close();
            source = null;
            setState("disconnected");
            reconnect();
        };
    }

    function reconnect() {
        var wait = delay + Math.random() * 500;
        console.log("[hamr] reconnecting in " + Math.round(wait) + "ms");
        setTimeout(connect, wait);
        delay = Math.min(delay * 2, MAX_DELAY);
    }

    function reloadCSS() {
        var links = document.querySelectorAll('link[rel="stylesheet"]');
        for (var i = 0; i < links.length; i++) {
            var link = links[i];
            var href = link.getAttribute("href");
            if (href) {
                var url = new URL(href, location.href);
                url.searchParams.set("_hamr", Date.now());
                link.setAttribute("href", url.toString());
            }
        }
        console.log("[hamr] CSS reloaded");
    }

    connect();
})();
