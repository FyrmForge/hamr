// hamr dev — SSE-based live reload client.
// Injected into HTML responses by the dev proxy.

(function() {
    "use strict";

    var MIN_DELAY = 1000;
    var MAX_DELAY = 30000;
    var delay = MIN_DELAY;
    var source = null;

    function connect() {
        source = new EventSource("/__hamr/reload");

        source.addEventListener("connected", function() {
            delay = MIN_DELAY;
            console.log("[hamr] live reload connected");
        });

        source.addEventListener("reload", function(e) {
            var scope = e.data || "full";

            if (scope === "css") {
                reloadCSS();
                return;
            }

            // Full reload: try morphing with Idiomorph if available.
            if (typeof Idiomorph !== "undefined") {
                fetch(location.href).then(function(resp) {
                    return resp.text();
                }).then(function(html) {
                    Idiomorph.morph(document.documentElement, html);
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
