// Dialogs for "Add a user", "Add an agent", "Add seller".
// htmx loads the form into #modal; this opens it as a modal <dialog>,
// closes it on Cancel / ✕ / Esc / a tap outside, and after a successful
// save (the server's "lotaria:done" event) shows a short confirmation.
(() => {
  const modal = () => document.querySelector("#modal > dialog");

  document.addEventListener("htmx:afterSwap", (e) => {
    if (e.detail.target.id !== "modal") return;
    const d = modal();
    if (!d || d.open) return;
    d.showModal();
    const first = d.querySelector("input:not([type=hidden]), select, textarea");
    if (first) first.focus();
  });

  // After a form re-renders with errors, put the cursor on the first problem.
  document.addEventListener("htmx:afterSwap", (e) => {
    const d = modal();
    if (!d || !d.contains(e.detail.elt)) return;
    const body = d.querySelector("form .overflow-y-auto");
    if (body) body.scrollTop = 0;
  });

  // A closed dialog is removed, so the next "Add" loads a fresh form.
  document.addEventListener("close", (e) => {
    if (e.target.matches && e.target.matches("#modal > dialog")) e.target.remove();
  }, true);

  document.addEventListener("click", (e) => {
    const d = modal();
    if (!d) return;
    if (e.target.closest("[data-modal-close]") && d.contains(e.target)) {
      e.preventDefault();
      d.close();
    } else if (e.target === d) {
      // A tap on the backdrop (outside the panel) closes the dialog,
      // unless something has been typed.
      const typed = [...d.querySelectorAll("input:not([type=hidden])")].some((i) => i.value);
      if (!typed) d.close();
    }
  });

  let hide;
  document.addEventListener("lotaria:done", (e) => {
    const d = modal();
    if (d) d.close();
    const box = document.getElementById("toast");
    if (!box) return;
    box.innerHTML = "";
    const t = document.createElement("div");
    t.className = "pointer-events-auto flex max-w-md items-center gap-3 rounded-xl bg-ink px-4 py-3 text-[15px] font-medium text-white shadow-xl";
    t.innerHTML = '<svg class="size-5 shrink-0 text-[#7EE0A8]" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"></path></svg>';
    const msg = document.createElement("span");
    msg.textContent = (e.detail && e.detail.value) || "Saved.";
    t.appendChild(msg);
    box.appendChild(t);
    clearTimeout(hide);
    hide = setTimeout(() => { box.innerHTML = ""; }, 4000);
  });
})();

// 4-digit result boxes ([data-advance]): digits only, jump to the next box
// after 4 digits, and a pasted list of numbers fills the following boxes
// (so all 23 results can be pasted at once, e.g. from a message).
(() => {
  const boxes = (el) => [...el.form.querySelectorAll("[data-advance]")];

  document.addEventListener("input", (e) => {
    const el = e.target;
    if (!el.matches || !el.matches("[data-advance]")) return;
    el.value = el.value.replace(/\D/g, "").slice(0, 4);
    if (el.value.length === 4 && e.inputType && e.inputType.startsWith("insert")) {
      const all = boxes(el), next = all[all.indexOf(el) + 1];
      if (next) { next.focus(); next.select(); }
    }
  });

  document.addEventListener("keydown", (e) => {
    const el = e.target;
    if (!el.matches || !el.matches("[data-advance]") || e.key !== "Backspace" || el.value) return;
    const all = boxes(el), prev = all[all.indexOf(el) - 1];
    if (prev) { e.preventDefault(); prev.focus(); }
  });

  document.addEventListener("paste", (e) => {
    const el = e.target;
    if (!el.matches || !el.matches("[data-advance]")) return;
    const nums = (e.clipboardData.getData("text") || "").match(/\d{4}/g);
    if (!nums || nums.length < 2) return; // a single number pastes normally
    e.preventDefault();
    const all = boxes(el);
    let i = all.indexOf(el);
    for (const n of nums) { if (i >= all.length) break; all[i++].value = n; }
    (all[Math.min(i, all.length - 1)]).focus();
  });
})();

// "More" menus in the navigation: close when tapping elsewhere or
// pressing Escape, and when another menu opens.
(() => {
  document.addEventListener("click", (e) => {
    document.querySelectorAll("details[data-menu][open]").forEach((d) => {
      if (!d.contains(e.target) || e.target.closest("a")) d.open = false;
    });
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") document.querySelectorAll("details[data-menu][open]").forEach((d) => (d.open = false));
  });
})();
