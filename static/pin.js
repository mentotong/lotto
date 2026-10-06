// PIN-style number boxes on the sell form: one digit per box, numbers
// only, the cursor moves on by itself. Numbers end in the last box: for 3D
// start typing in the 2nd box, for 2D in the 3rd (like ✕623 on the slip).
// Uses event delegation so it keeps working after htmx swaps content.
(() => {
  const isPin = (el) => el instanceof HTMLInputElement && el.closest("[data-pin]");
  const boxesOf = (el) => [...el.closest("[data-pin]").querySelectorAll("input")];
  const lines = () => [...document.querySelectorAll("#lines [data-pin]")];
  const recalc = () => {
    const t = document.getElementById("lines");
    if (t) t.dispatchEvent(new Event("input", { bubbles: true }));
  };

  // After "Add line" (from the keyboard), focus the new line once it's in.
  let focusNewLine = false;

  // Move to the next line's first box, adding a line if this was the last.
  const goToNextLine = (el) => {
    const all = lines();
    const next = all[all.indexOf(el.closest("[data-pin]")) + 1];
    if (next) {
      next.querySelector("input").focus();
      return;
    }
    const add = document.getElementById("add-line");
    if (add && !add.disabled) {
      focusNewLine = true;
      add.click();
    }
  };

  document.addEventListener("htmx:afterSettle", (e) => {
    if (!focusNewLine || e.detail.target.id !== "lines") return;
    focusNewLine = false;
    const all = lines();
    if (all.length) all[all.length - 1].querySelector("input").focus();
  });

  // Only allow digits to be typed.
  document.addEventListener("beforeinput", (e) => {
    if (!isPin(e.target)) return;
    if (e.inputType.startsWith("insert") && e.data && /\D/.test(e.data)) e.preventDefault();
  });

  document.addEventListener("input", (e) => {
    const el = e.target;
    if (!isPin(el) || !e.isTrusted) return;
    const digit = el.value.replace(/\D/g, "").slice(-1); // keep the last digit typed
    el.value = digit;
    if (!digit) return;
    const boxes = boxesOf(el), i = boxes.indexOf(el);
    if (i < boxes.length - 1) {
      boxes[i + 1].focus();
      boxes[i + 1].select();
    } else {
      goToNextLine(el);
    }
  });

  document.addEventListener("keydown", (e) => {
    const el = e.target;
    if (!isPin(el)) return;
    const boxes = boxesOf(el), i = boxes.indexOf(el);
    if (e.key === "Backspace" && el.value === "" && i > 0) {
      e.preventDefault();
      boxes[i - 1].value = "";
      boxes[i - 1].focus();
      recalc();
    } else if (e.key === "ArrowLeft" && i > 0) {
      e.preventDefault(); boxes[i - 1].focus(); boxes[i - 1].select();
    } else if (e.key === "ArrowRight" && i < boxes.length - 1) {
      e.preventDefault(); boxes[i + 1].focus(); boxes[i + 1].select();
    } else if (e.key === "Enter") {
      e.preventDefault(); // don't sell by accident; go to the next line instead
      goToNextLine(el);
    }
  });

  // Pasting fills the line so the number ends in the last box:
  // "7632" → 7632, "623" → ✕623, "23" → ✕✕23.
  document.addEventListener("paste", (e) => {
    const el = e.target;
    if (!isPin(el)) return;
    e.preventDefault();
    const boxes = boxesOf(el);
    const digits = (e.clipboardData.getData("text") || "").replace(/\D/g, "").slice(-boxes.length);
    if (!digits) return;
    const start = boxes.length - digits.length;
    boxes.forEach((b, i) => { b.value = i < start ? "" : digits[i - start]; });
    recalc();
    goToNextLine(el);
  });

  // ✕ removes a line (the last remaining line is just cleared).
  document.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-remove-line]");
    if (!btn) return;
    const row = btn.closest("[data-line]");
    if (document.querySelectorAll("#lines [data-line]").length > 1) {
      row.remove();
    } else {
      row.querySelectorAll("input:not([type=hidden])").forEach((i) => { i.value = ""; });
    }
    recalc();
  });
})();
