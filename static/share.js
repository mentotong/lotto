// "Share with buyer" on the receipt page: draws the ticket and its QR code
// into a PNG, then lets the seller send it (phone share sheet), save it,
// or copy the ticket's check link. Event delegation keeps it working after
// htmx page swaps.
(() => {
  const C = {
    paper: "#F3F5FA", form: "#2340A0", ink: "#17203B", muted: "#5E6787",
    rule: "#9AA6CC", pen: "#D0263A", white: "#FFFFFF",
  };
  // The app's name, logo and colours (App setup) come with the data.
  const B = { name: "Lotaria", tagline: "", note: "", contact: "", slug: "lotaria" };
  const useBrand = (b) => {
    Object.assign(B, b || {});
    if (B.color) C.form = B.color;
    if (B.accent) C.pen = B.accent;
  };
  const rgba = (hex, a) => {
    const n = parseInt(hex.slice(1), 16);
    return `rgba(${n >> 16 & 255},${n >> 8 & 255},${n & 255},${a})`;
  };
  const SANS = '"Archivo", system-ui, sans-serif';
  const HAND = '"Kalam", cursive';
  const $ = (id) => document.getElementById(id);

  let made = null; // { serial, file, url }

  // The page says what to share: a ticket (#ticket-data) or a draw's
  // results (#results-data).
  const shareData = () => {
    const el = $("ticket-data") || $("results-data");
    if (!el) return null;
    const d = JSON.parse(el.textContent);
    d.kind = d.kind || "ticket";
    d.key = d.kind + ":" + (d.serial || d.date) + ":" + JSON.stringify(d.brand || {});
    return d;
  };

  const say = (msg) => { const s = $("share-status"); if (s) s.textContent = msg; };

  // The QR code is already on the page as SVG; turn it into an image.
  const qrImage = () => new Promise((resolve, reject) => {
    const svg = document.querySelector("[data-share-qr] svg");
    if (!svg) return reject(new Error("no QR code on the page"));
    const src = new XMLSerializer().serializeToString(svg);
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = reject;
    img.src = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(src);
  });

  const fit = (ctx, text, max) => {
    if (ctx.measureText(text).width <= max) return text;
    while (text.length > 1 && ctx.measureText(text + "…").width > max) text = text.slice(0, -1);
    return text + "…";
  };

  const wrap = (ctx, text, max) => {
    const out = []; let line = "";
    for (const part of text.split(/(?<=[\/\s])/)) {
      if (ctx.measureText(line + part).width > max && line) { out.push(line); line = part; }
      else line += part;
    }
    if (line) out.push(line);
    return out;
  };

  const ticketIcon = (ctx, x, y, size) => {
    ctx.save();
    ctx.translate(x, y); ctx.scale(size / 24, size / 24);
    ctx.strokeStyle = C.form; ctx.lineWidth = 1.8; ctx.lineCap = "round"; ctx.lineJoin = "round";
    ctx.stroke(new Path2D("M3.5 7.5A1.5 1.5 0 0 1 5 6h14a1.5 1.5 0 0 1 1.5 1.5V10a2 2 0 0 0 0 4v2.5A1.5 1.5 0 0 1 19 18H5a1.5 1.5 0 0 1-1.5-1.5V14a2 2 0 0 0 0-4z"));
    ctx.setLineDash([1.5, 2]);
    ctx.stroke(new Path2D("M14.5 6.5v11"));
    ctx.restore();
  };

  const roundRect = (ctx, x, y, w, h, r) => { ctx.beginPath(); ctx.roundRect(x, y, w, h, r); };

  const loadImage = (src) => new Promise((resolve) => {
    if (!src) return resolve(null);
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => resolve(null); // fall back to the ticket icon
    img.src = src;
  });

  // logoTile draws the logo (or the ticket icon) on a white rounded tile.
  const logoTile = (ctx, logo, x, y, size, r) => {
    roundRect(ctx, x, y, size, size, r); ctx.fillStyle = C.white; ctx.fill();
    if (!logo) return ticketIcon(ctx, x + size / 6, y + size / 6, size * 2 / 3);
    const pad = size * 0.08, box = size - 2 * pad;
    const k = Math.min(box / logo.naturalWidth, box / logo.naturalHeight);
    const w = logo.naturalWidth * k, h = logo.naturalHeight * k;
    ctx.save(); ctx.imageSmoothingEnabled = true; ctx.imageSmoothingQuality = "high";
    ctx.drawImage(logo, x + (size - w) / 2, y + (size - h) / 2, w, h);
    ctx.restore();
  };

  async function draw(d) {
    useBrand(d.brand);
    const logo = await loadImage(B.logo);
    return d.kind === "results" ? drawResults(d, logo) : drawTicket(d, logo);
  }

  // --- results image: the results board, ready for WhatsApp & co ----------
  async function drawResults(d, logo) {
    await Promise.all([
      document.fonts.load(`800 52px ${SANS}`), document.fonts.load(`700 28px ${SANS}`),
    ]).catch(() => {});
    const qr = await qrImage();
    const W = 1080, M = 40, X = M, SW = W - 2 * M, P = 28;     // canvas, margin, card, padding
    const H = 1610;
    const cv = document.createElement("canvas");
    cv.width = W; cv.height = H;
    const ctx = cv.getContext("2d");
    const spaced = (px) => { if ("letterSpacing" in ctx) ctx.letterSpacing = px; };
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, H);

    // card
    ctx.save();
    ctx.shadowColor = "rgba(23,32,59,0.18)"; ctx.shadowBlur = 40; ctx.shadowOffsetY = 16;
    roundRect(ctx, X, M, SW, H - 2 * M, 28); ctx.fillStyle = C.white; ctx.fill();
    ctx.restore();
    ctx.save(); roundRect(ctx, X, M, SW, H - 2 * M, 28); ctx.clip();

    // header: brand and "Results", then the draw date
    let y = M;
    ctx.fillStyle = C.form; ctx.fillRect(X, y, SW, 250);
    logoTile(ctx, logo, X + 44, y + 40, 72, 14);
    ctx.font = `600 30px ${SANS}`;
    const resultsW = ctx.measureText("Results").width;
    ctx.fillStyle = C.white; ctx.font = `800 52px ${SANS}`;
    ctx.fillText(fit(ctx, B.name, SW - 44 - 140 - resultsW - 40), X + 140, y + 94);
    ctx.globalAlpha = 0.8; ctx.font = `600 30px ${SANS}`; ctx.textAlign = "right";
    ctx.fillText("Results", X + SW - 44, y + 90); ctx.globalAlpha = 1;
    ctx.fillStyle = "rgba(255,255,255,0.18)"; ctx.fillRect(X + 44, y + 140, SW - 88, 2);
    ctx.fillStyle = C.white; ctx.textAlign = "center"; ctx.font = `800 64px ${SANS}`;
    ctx.fillText(d.label, X + SW / 2, y + 220);
    ctx.textAlign = "left";
    y += 250;

    // board
    ctx.fillStyle = C.paper; ctx.fillRect(X, y, SW, H - M - y);
    y += P;
    const innerW = SW - 2 * P, ix = X + P;
    const labelW = Math.round(innerW * 0.42), gap = 18, rowH = 136;
    for (const [label, num] of [["1st", d.first], ["2nd", d.second], ["3rd", d.third]]) {
      roundRect(ctx, ix, y, labelW, rowH, 20); ctx.fillStyle = C.form; ctx.fill();
      ctx.fillStyle = C.white; ctx.font = `800 64px ${SANS}`; ctx.textAlign = "center";
      ctx.fillText(label, ix + labelW / 2, y + rowH / 2 + 22);
      const nx = ix + labelW + gap, nw = innerW - labelW - gap;
      roundRect(ctx, nx, y, nw, rowH, 20); ctx.fillStyle = C.white; ctx.fill();
      ctx.strokeStyle = rgba(C.form, 0.15); ctx.lineWidth = 4; ctx.stroke();
      ctx.fillStyle = C.ink; ctx.font = `800 96px ${SANS}`; spaced("6px");
      ctx.fillText(num, nx + nw / 2 + 3, y + rowH / 2 + 34); spaced("0px");
      ctx.textAlign = "left";
      y += rowH + gap;
    }

    // Starter and Consolation, ten numbers each in two columns
    y += 6;
    const colW = (innerW - gap) / 2, headH = 80, cellH = 92;
    for (const [i, title, nums] of [[0, "Starter", d.starter], [1, "Consolation", d.consolation]]) {
      const gx = ix + i * (colW + gap);
      ctx.save();
      roundRect(ctx, gx, y, colW, headH + 5 * cellH, 20); ctx.fillStyle = C.white; ctx.fill();
      ctx.strokeStyle = rgba(C.form, 0.15); ctx.lineWidth = 4; ctx.stroke(); ctx.clip();
      ctx.fillStyle = C.form; ctx.fillRect(gx, y, colW, headH);
      ctx.fillStyle = C.white; ctx.font = `700 40px ${SANS}`; ctx.textAlign = "center";
      ctx.fillText(title, gx + colW / 2, y + 54);
      ctx.fillStyle = C.rule;
      ctx.fillRect(gx + colW / 2 - 1, y + headH, 2, 5 * cellH);
      for (let r = 1; r < 5; r++) ctx.fillRect(gx, y + headH + r * cellH, colW, 2);
      ctx.fillStyle = C.ink; ctx.font = `700 54px ${SANS}`; spaced("2px");
      nums.forEach((n, k) => {
        const cx = gx + (k % 2 === 0 ? colW / 4 : (3 * colW) / 4);
        const cy = y + headH + Math.floor(k / 2) * cellH + cellH / 2 + 19;
        ctx.fillText(n, cx, cy);
      });
      spaced("0px"); ctx.textAlign = "left";
      ctx.restore();
    }
    y += headH + 5 * cellH + 30;

    // footer: QR code and link to these results
    roundRect(ctx, ix, y, innerW, H - M - P - y, 20); ctx.fillStyle = C.white; ctx.fill();
    const qs = 150;
    ctx.imageSmoothingEnabled = false;
    ctx.drawImage(qr, ix + 24, y + 22, qs, qs);
    const tx = ix + 24 + qs + 32;
    ctx.fillStyle = C.ink; ctx.font = `800 36px ${SANS}`; ctx.fillText("Check all results", tx, y + 72);
    ctx.fillStyle = C.muted; ctx.font = `500 26px ${SANS}`;
    const short = d.link.replace(/^https?:\/\//, "");
    ctx.fillText(fit(ctx, short, ix + innerW - 24 - tx), tx, y + 118);
    ctx.fillStyle = C.form; ctx.font = `600 24px ${SANS}`;
    ctx.fillText(fit(ctx, B.contact || "Scan the code or open the link", ix + innerW - 24 - tx), tx, y + 156);
    ctx.restore();

    return await new Promise((res) => cv.toBlob(res, "image/png"));
  }

  // --- ticket image ---------------------------------------------------------
  async function drawTicket(d, logo) {
    await Promise.all([
      document.fonts.load(`800 52px ${SANS}`), document.fonts.load(`600 28px ${SANS}`),
      document.fonts.load(`700 44px ${HAND}`),
    ]).catch(() => {});
    const qr = await qrImage();

    const W = 1080, M = 48, X = M, SW = W - 2 * M;        // canvas, margin, slip
    const L = X + 48, R = X + SW - 48;                    // content edges
    const fields = [["Draw date", d.drawDate], ["Agent", d.agent || "—"], ["Seller", d.seller]];
    if (d.buyer) fields.push(["Buyer", d.buyer]);
    const foot = (B.note ? 1 : 0) + (B.contact ? 1 : 0);           // lines under the QR panel
    const H = M + 150 + 36 + fields.length * 76 + 36 + 56 + d.lines.length * 108 + 180 + 340 + (foot ? 40 + foot * 40 : 0) + M;

    const cv = document.createElement("canvas");
    cv.width = W; cv.height = H;
    const ctx = cv.getContext("2d");
    ctx.fillStyle = C.paper; ctx.fillRect(0, 0, W, H);

    // the slip
    ctx.save();
    ctx.shadowColor = "rgba(23,32,59,0.18)"; ctx.shadowBlur = 40; ctx.shadowOffsetY = 16;
    ctx.fillStyle = C.white; ctx.fillRect(X, M, SW, H - 2 * M);
    ctx.restore();

    // header band
    let y = M;
    ctx.fillStyle = C.form; ctx.fillRect(X, y, SW, 150);
    logoTile(ctx, logo, L, y + 39, 72, 12);
    ctx.fillStyle = C.white; ctx.textBaseline = "alphabetic";
    ctx.font = `700 46px ${SANS}`;
    const nameW = R - ctx.measureText(d.serial).width - 40 - (L + 96);
    ctx.font = `800 52px ${SANS}`; ctx.fillText(fit(ctx, B.name, nameW), L + 96, B.tagline ? y + 82 : y + 94);
    ctx.globalAlpha = 0.75; ctx.font = `500 26px ${SANS}`;
    if (B.tagline) ctx.fillText(fit(ctx, B.tagline, nameW), L + 96, y + 118);
    ctx.textAlign = "right"; ctx.fillText("Serial no.", R, y + 66); ctx.globalAlpha = 1;
    ctx.font = `700 46px ${SANS}`; ctx.fillText(d.serial, R, y + 118);
    ctx.textAlign = "left";
    y += 150 + 36;

    // written fields on ruled lines
    for (const [label, value] of fields) {
      ctx.fillStyle = C.form; ctx.font = `600 28px ${SANS}`; ctx.fillText(label, L, y + 44);
      ctx.fillStyle = C.pen; ctx.font = `700 40px ${HAND}`; ctx.textAlign = "right";
      ctx.fillText(fit(ctx, value, SW - 340), R, y + 44); ctx.textAlign = "left";
      ctx.fillStyle = C.rule; ctx.fillRect(L, y + 60, R - L, 2);
      y += 76;
    }
    y += 36;

    // numbers table: the same five columns as the receipt page
    const col = { game: L + 300, qty: L + 440, price: R - 190, amount: R };
    ctx.fillStyle = C.form; ctx.fillRect(L, y, R - L, 4); ctx.fillRect(L, y + 52, R - L, 4);
    ctx.font = `600 26px ${SANS}`; ctx.fillText("Number", L, y + 37);
    ctx.fillText("Game", col.game, y + 37);
    ctx.textAlign = "center"; ctx.fillText("Qty", col.qty, y + 37);
    ctx.textAlign = "right"; ctx.fillText("Price", col.price, y + 37); ctx.fillText("Amount", col.amount, y + 37);
    ctx.textAlign = "left";
    y += 56;
    for (const line of d.lines) {
      const digits = line.number.padStart(4, "✕");
      for (let i = 0; i < 4; i++) {
        const bx = L + i * 70, by = y + 14;
        ctx.strokeStyle = C.rule; ctx.lineWidth = 2; ctx.strokeRect(bx + 1, by + 1, 60, 78);
        const ch = digits[i];
        ctx.fillStyle = C.pen; ctx.textAlign = "center";
        if (ch === "✕") { ctx.globalAlpha = 0.55; ctx.font = `700 36px ${HAND}`; ctx.fillText("✕", bx + 31, by + 54); ctx.globalAlpha = 1; }
        else { ctx.font = `700 52px ${HAND}`; ctx.fillText(ch, bx + 31, by + 62); }
        ctx.textAlign = "left";
      }
      const mid = y + 66;
      ctx.fillStyle = C.form; ctx.font = `700 32px ${SANS}`; ctx.fillText(line.game, col.game, mid);
      ctx.fillStyle = C.ink; ctx.font = `600 30px ${SANS}`; ctx.textAlign = "center"; ctx.fillText(line.qty, col.qty, mid);
      ctx.fillStyle = C.muted; ctx.font = `500 28px ${SANS}`; ctx.textAlign = "right"; ctx.fillText(line.price, col.price, mid);
      ctx.fillStyle = C.pen; ctx.font = `700 44px ${HAND}`; ctx.fillText(line.amount, col.amount, mid + 2);
      ctx.textAlign = "left";
      ctx.fillStyle = "rgba(154,166,204,0.6)"; ctx.fillRect(L, y + 106, R - L, 2);
      y += 108;
    }

    // total
    ctx.fillStyle = C.muted; ctx.font = `500 26px ${SANS}`;
    ctx.fillText(`Sold ${d.sold}`, L, y + 110);
    ctx.fillText(`${d.lines.length} ${d.lines.length === 1 ? "number" : "numbers"}`, L, y + 146);
    ctx.textAlign = "right";
    ctx.fillStyle = C.form; ctx.font = `600 28px ${SANS}`; ctx.fillText("Total", R, y + 60);
    ctx.fillStyle = C.pen; ctx.font = `700 96px ${HAND}`; ctx.fillText(d.total, R, y + 150);
    ctx.textAlign = "left";
    y += 180;

    // QR code panel, behind a dashed tear line
    ctx.strokeStyle = C.rule; ctx.lineWidth = 3; ctx.setLineDash([12, 10]);
    ctx.beginPath(); ctx.moveTo(X, y); ctx.lineTo(X + SW, y); ctx.stroke(); ctx.setLineDash([]);
    const qs = 260;
    ctx.imageSmoothingEnabled = false;
    ctx.drawImage(qr, L, y + 40, qs, qs);
    const tx = L + qs + 40, tw = R - tx;
    ctx.fillStyle = C.ink; ctx.font = `800 36px ${SANS}`; ctx.fillText("Scan to check", tx, y + 92);
    ctx.fillText("this ticket", tx, y + 134);
    ctx.fillStyle = C.muted; ctx.font = `500 24px ${SANS}`;
    let ly = y + 184;
    for (const part of wrap(ctx, d.link.replace(/^https?:\/\//, ""), tw).slice(0, 3)) { ctx.fillText(part, tx, ly); ly += 32; }
    ctx.fillStyle = C.form; ctx.font = `600 24px ${SANS}`; ctx.fillText("Keep this image as your receipt.", tx, Math.max(ly + 16, y + 290));
    y += 340;

    // receipt note and contact (App setup)
    if (foot) {
      ctx.fillStyle = "rgba(154,166,204,0.6)"; ctx.fillRect(L, y, R - L, 2);
      ctx.textAlign = "center"; let fy = y + 50;
      if (B.note) { ctx.fillStyle = C.form; ctx.font = `700 26px ${SANS}`; ctx.fillText(fit(ctx, B.note, R - L), X + SW / 2, fy); fy += 40; }
      if (B.contact) { ctx.fillStyle = C.muted; ctx.font = `500 24px ${SANS}`; ctx.fillText(fit(ctx, B.contact, R - L), X + SW / 2, fy); }
      ctx.textAlign = "left";
    }

    const blob = await new Promise((res) => cv.toBlob(res, "image/png"));
    return blob;
  }

  const shareText = (d) => d.kind === "results"
    ? `${B.name} results for ${d.label}: 1st ${d.first}, 2nd ${d.second}, 3rd ${d.third}. All results: ${d.link}`
    : `${B.name} ticket ${d.serial}. Check it here: ${d.link}`;

  async function open() {
    const d = shareData(), dlg = $("share-dialog");
    if (!d || !dlg) return;
    useBrand(d.brand);
    dlg.showModal();
    say("");
    const img = $("share-preview"), loading = $("share-loading");
    const wa = $("share-whatsapp"), fb = $("share-facebook");
    if (wa) wa.href = "https://wa.me/?text=" + encodeURIComponent(shareText(d));
    if (fb) fb.href = "https://www.facebook.com/sharer/sharer.php?u=" + encodeURIComponent(d.link);
    if (!made || made.key !== d.key) {
      img.classList.add("hidden"); loading.classList.remove("hidden");
      try {
        const blob = await draw(d);
        if (made) URL.revokeObjectURL(made.url);
        const name = d.kind === "results" ? `${B.slug}-results-${d.date}.png` : `${B.slug}-ticket-${d.serial}.png`;
        made = {
          key: d.key, data: d,
          file: new File([blob], name, { type: "image/png" }),
          url: URL.createObjectURL(blob),
        };
      } catch (err) {
        loading.textContent = "Couldn't make the image. You can still copy the link.";
        return;
      }
    }
    img.src = made.url; img.classList.remove("hidden"); loading.classList.add("hidden");
    const canSend = !!(navigator.canShare && navigator.canShare({ files: [made.file] }));
    $("share-native").classList.toggle("hidden", !canSend);
  }

  async function send() {
    if (!made) return;
    try {
      const d = made.data;
      await navigator.share({
        files: [made.file],
        title: d.kind === "results" ? `${B.name} results ${d.label}` : `${B.name} ticket ${d.serial}`,
        text: shareText(d),
      });
      say("Sent.");
    } catch (err) {
      if (err && err.name !== "AbortError") say("Couldn't open sharing. Save the image instead.");
    }
  }

  function save() {
    if (!made) return;
    const a = document.createElement("a");
    a.href = made.url; a.download = made.file.name;
    document.body.appendChild(a); a.click(); a.remove();
    say("Image saved to your downloads.");
  }

  async function copy() {
    const d = shareData();
    if (!d) return;
    try {
      await navigator.clipboard.writeText(d.link);
    } catch {
      const t = document.createElement("textarea");
      t.value = d.link; document.body.appendChild(t); t.select();
      document.execCommand("copy"); t.remove();
    }
    say("Link copied.");
  }

  document.addEventListener("click", (e) => {
    const t = e.target;
    if (t.closest("[data-share-open]")) open();
    else if (t.closest("[data-share-close]")) $("share-dialog").close();
    else if (t.closest("#share-native")) send();
    else if (t.closest("#share-save")) save();
    else if (t.closest("#share-copy")) copy();
    else if (t.id === "share-dialog") t.close(); // tap outside the sheet
  });
})();
