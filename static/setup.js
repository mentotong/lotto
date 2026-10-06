// App setup page: keeps the colour pickers and hex fields in step, and
// updates the preview while the admin types. Delegated, so it works after
// htmx swaps the page in.
(() => {
  const $ = (id) => document.getElementById(id);
  const hex = (v) => {
    v = (v || "").trim().replace(/^#?/, "#");
    if (/^#[0-9a-f]{3}$/i.test(v)) v = "#" + [...v.slice(1)].map((c) => c + c).join("");
    return /^#[0-9a-f]{6}$/i.test(v) ? v.toUpperCase() : null;
  };
  const shade = (h, k) => "#" + [1, 3, 5].map((i) =>
    Math.round(parseInt(h.slice(i, i + 2), 16) * (1 - k)).toString(16).padStart(2, "0")).join("");
  const val = (form, name) => (form.elements[name] ? form.elements[name].value : "").trim();

  let objectURL = null;

  function update(form, changed) {
    const pv = $("brand-preview");
    if (!pv) return;
    const c = hex(val(form, "color")), a = hex(val(form, "accent"));
    if (c) { pv.style.setProperty("--color-form", c); pv.style.setProperty("--color-form-dark", shade(c, 0.25)); }
    if (a) { pv.style.setProperty("--color-pen", a); pv.style.setProperty("--color-pen-dark", shade(a, 0.16)); }
    $("pv-name").textContent = val(form, "name") || "—";
    $("pv-tagline").textContent = val(form, "tagline");
    $("pv-note").textContent = val(form, "receipt_note");
    $("pv-contact").textContent = [val(form, "phone"), val(form, "address")].filter(Boolean).join(" · ");
    const cur = val(form, "currency") || "$";
    $("pv-total").textContent = val(form, "currency_pos") === "after" ? "2.50 " + cur : cur + "2.50";

    // Logo: a newly picked file, the saved logo, or the ticket icon.
    const img = $("pv-logo"), icon = $("pv-icon");
    const file = form.elements.logo && form.elements.logo.files[0];
    if (changed && changed.name === "logo") {
      if (objectURL) URL.revokeObjectURL(objectURL);
      objectURL = file ? URL.createObjectURL(file) : null;
    }
    const remove = form.elements.remove_logo && form.elements.remove_logo.checked;
    const src = objectURL || (!remove && img.dataset.saved) || "";
    if (src) img.src = src;
    img.classList.toggle("hidden", !src);
    icon.classList.toggle("hidden", !!src);
  }

  document.addEventListener("input", (e) => {
    const form = e.target.closest && e.target.closest("#brand-form");
    if (!form) return;
    const t = e.target;
    if (t.dataset.colourFor) form.elements[t.dataset.colourFor].value = t.value.toUpperCase();
    else if (t.name === "color" || t.name === "accent") {
      const h = hex(t.value), picker = form.querySelector(`[data-colour-for="${t.name}"]`);
      if (h && picker) picker.value = h.toLowerCase();
    }
    update(form, t);
  });
  document.addEventListener("change", (e) => {
    const form = e.target.closest && e.target.closest("#brand-form");
    if (form && (e.target.type === "file" || e.target.type === "checkbox" || e.target.tagName === "SELECT")) update(form, e.target);
  });
})();
