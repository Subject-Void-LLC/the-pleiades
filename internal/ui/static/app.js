/*
 * Pleiades control plane -- the entire client-side behaviour set.
 *
 * Three behaviours, and the count is a design constraint rather than a
 * coincidence. Everything else this UI does happens on the server: the
 * page is rendered whole, HTMX swaps fragments the server produced, and
 * nothing here holds state, computes authorization, or decides what a user
 * may do. A fourth behaviour is a signal that logic has started leaking
 * back to the client.
 *
 * All three exist to repair something an HTMX swap breaks that a full page
 * load gets for free. A browser navigating to a new page moves focus and
 * announces the new title on its own; a fragment silently replaced in
 * place does neither, which strands a screen reader user in content that
 * quietly changed under them. This is the server-rendered form of the same
 * problem an SPA has with client-side routing.
 */
(function () {
  "use strict";

  /* ---- 1. Announce what a swap changed ----
   *
   * The live region must already be in the DOM and empty. A region
   * inserted together with its text is not announced at all: the screen
   * reader has nothing to observe changing. So the layout renders an empty
   * one on every page and this only ever writes into it.
   */
  function announce(message) {
    var region = document.getElementById("live-announcer");
    if (!region || !message) {
      return;
    }
    // Clearing first forces an announcement even when the new text is
    // identical to the old, which is the common case for "12 results".
    region.textContent = "";
    window.setTimeout(function () {
      region.textContent = message;
    }, 50);
  }

  document.body.addEventListener("htmx:afterSwap", function (event) {
    var target = event.detail && event.detail.target;
    if (!target) {
      return;
    }

    /* ---- 2. Move focus deliberately ----
     *
     * A swapped region that declares itself a focus target receives
     * focus, so the next Tab continues from the content that just
     * changed rather than from wherever the trigger happened to be.
     * tabindex="-1" makes it programmatically focusable without adding it
     * to the tab order, which is the whole reason that value exists.
     */
    if (target.hasAttribute("data-focus-after-swap")) {
      target.setAttribute("tabindex", "-1");
      target.focus({ preventScroll: true });
    }

    announce(target.getAttribute("data-announce"));
  });

  /* ---- 3. Dialogs ----
   *
   * Native <dialog> with showModal() brings the focus trap, the Escape
   * handler, the inert background and the correct role with it. A
   * hand-built modal has to reimplement all four and typically gets the
   * focus return wrong, which is the part users actually notice.
   *
   * The one thing showModal() does not do is put focus back where it came
   * from, so that is tracked here.
   */
  var lastTrigger = null;

  document.addEventListener("click", function (event) {
    var opener = event.target.closest("[data-dialog-open]");
    if (opener) {
      var dialog = document.getElementById(opener.getAttribute("data-dialog-open"));
      if (dialog && typeof dialog.showModal === "function") {
        event.preventDefault();
        lastTrigger = opener;
        dialog.showModal();
      }
      return;
    }

    var closer = event.target.closest("[data-dialog-close]");
    if (closer) {
      var open = closer.closest("dialog");
      if (open) {
        event.preventDefault();
        open.close();
      }
    }
  });

  document.addEventListener(
    "close",
    function (event) {
      if (event.target.tagName === "DIALOG" && lastTrigger) {
        // Returning focus to the trigger is what stops a keyboard user
        // being dropped at the top of the document every time they
        // dismiss a confirmation.
        lastTrigger.focus();
        lastTrigger = null;
      }
    },
    true
  );
})();
