"use strict";

// ---- Yardımcılar ----

const $ = (sec, kok = document) => kok.querySelector(sec);
const $$ = (sec, kok = document) => [...kok.querySelectorAll(sec)];

/** Güvenli DOM üretici: metinler her zaman textContent ile yazılır (XSS'e karşı). */
function h(etiket, ozellikler = {}, ...cocuklar) {
  const el = document.createElement(etiket);
  for (const [k, v] of Object.entries(ozellikler)) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of cocuklar.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

const paraBicimi = new Intl.NumberFormat("tr-TR", { style: "currency", currency: "TRY" });
const tarihBicimi = new Intl.DateTimeFormat("tr-TR", { dateStyle: "short", timeStyle: "short" });
const para = (v) => paraBicimi.format(Number(v));
const tarih = (v) => tarihBicimi.format(new Date(v));

/**
 * Kullanıcının yazdığı tutarı ("1.250,50", "1250.5", "250") API'nin beklediği
 * "1250.50" biçimine çevirir. Geçersizse null döner. Float kullanılmaz.
 */
function tutarCevir(metin) {
  let s = String(metin).trim().replace(/\s|₺/g, "");
  if (s.includes(",")) s = s.replace(/\./g, "").replace(",", ".");
  if (!/^\d{1,12}(\.\d{1,2})?$/.test(s)) return null;
  return s;
}

const DURUMLAR = {
  basarili: "Başarılı",
  reddedildi: "Reddedildi",
  kismi_iade: "Kısmi iade",
  iade_edildi: "İade edildi",
};

// ---- Oturum ----

const oturum = {
  get token() { return sessionStorage.getItem("token"); },
  get kullanici() {
    try { return JSON.parse(sessionStorage.getItem("kullanici")); } catch { return null; }
  },
  ac(token, kullanici) {
    sessionStorage.setItem("token", token);
    sessionStorage.setItem("kullanici", JSON.stringify(kullanici));
  },
  kapat() { sessionStorage.clear(); },
  get admin() { return this.kullanici?.rol === "admin"; },
};

class ApiHatasi extends Error {
  constructor(durum, veri) {
    super(veri?.mesaj || `İstek başarısız (${durum})`);
    this.durum = durum;
    this.kod = veri?.kod;
    this.veri = veri;
  }
}

async function api(metot, yol, govde, { idempotencyKey } = {}) {
  const basliklar = { Accept: "application/json" };
  if (govde !== undefined) basliklar["Content-Type"] = "application/json";
  if (oturum.token) basliklar.Authorization = "Bearer " + oturum.token;
  if (idempotencyKey) basliklar["Idempotency-Key"] = idempotencyKey;

  const yanit = await fetch(yol, {
    method: metot,
    headers: basliklar,
    body: govde === undefined ? undefined : JSON.stringify(govde),
  });
  const veri = await yanit.json().catch(() => null);

  if (yanit.status === 401 && yol !== "/api/v1/auth/login") {
    cikisYap("Oturumunuzun süresi doldu, lütfen tekrar giriş yapın.");
    throw new ApiHatasi(401, veri);
  }
  if (!yanit.ok) throw new ApiHatasi(yanit.status, veri);
  return veri;
}

// ---- Arayüz parçaları ----

function bildir(mesaj, tur = "basari") {
  const el = h("div", { class: "bildirim" + (tur === "hata" ? " hata" : ""), role: tur === "hata" ? "alert" : "status" }, mesaj);
  $("#bildirimler").append(el);
  setTimeout(() => el.remove(), 4500);
}

function formHatasi(form, mesaj) {
  const el = $(".form-hata", form);
  el.textContent = mesaj || "";
  el.hidden = !mesaj;
}

/** Form gönderilirken düğmeyi kilitler; çift tıklamayla çift istek gitmesini önler. */
async function mesgul(form, fn) {
  const dugme = $("button[type=submit]", form);
  dugme.disabled = true;
  try { return await fn(); } finally { dugme.disabled = false; }
}

function hataMesaji(err) {
  if (err instanceof ApiHatasi) return err.message;
  return "Sunucuya ulaşılamadı. Bağlantınızı kontrol edip tekrar deneyin.";
}

// ---- Giriş / çıkış ----

$("#girisFormu").addEventListener("submit", (e) => {
  e.preventDefault();
  const form = e.currentTarget;
  formHatasi(form);
  const eposta = form.eposta.value.trim();
  const sifre = form.sifre.value;
  if (!eposta || !sifre) return formHatasi(form, "E-posta ve şifre zorunlu.");

  mesgul(form, async () => {
    try {
      const v = await api("POST", "/api/v1/auth/login", { eposta, sifre });
      oturum.ac(v.token, v.kullanici);
      form.reset();
      uygulamayiAc();
    } catch (err) {
      formHatasi(form, err.kod === "cok_fazla_istek"
        ? "Çok fazla deneme yaptınız. Bir dakika sonra tekrar deneyin."
        : hataMesaji(err));
    }
  });
});

function cikisYap(mesaj) {
  oturum.kapat();
  durum.kartlar = [];
  $("#uygulama").hidden = true;
  $("#girisEkrani").hidden = false;
  formHatasi($("#girisFormu"), mesaj);
  $("#girisFormu").eposta.focus();
}

$("#cikisBtn").addEventListener("click", () => cikisYap());

// ---- Uygulama ----

const durum = { kartlar: [], imlec: null, iadeOdemesi: null };

function uygulamayiAc() {
  const k = oturum.kullanici;
  $("#girisEkrani").hidden = true;
  $("#uygulama").hidden = false;
  $("#kullaniciEposta").textContent = k.eposta;
  const rol = $("#kullaniciRol");
  rol.textContent = k.rol === "admin" ? "Admin" : "Kullanıcı";
  rol.className = "rozet" + (k.rol === "admin" ? " admin" : "");
  $$("[data-admin]").forEach((el) => { el.hidden = !oturum.admin; });
  sekmeAc("odemeler");
  kartlariYukle();
  odemeleriYukle();
  if (oturum.admin) mutabakatYukle();
}

function sekmeAc(ad) {
  if (ad === "yonetim" && !oturum.admin) ad = "odemeler";
  $$(".sekme").forEach((b) => b.toggleAttribute("aria-current", b.dataset.sekme === ad));
  $$(".sekme[aria-current]").forEach((b) => b.setAttribute("aria-current", "page"));
  for (const b of ["odemeler", "kartlar", "yonetim"]) $("#bolum-" + b).hidden = b !== ad;
  if (ad === "yonetim") kullanicilariYukle();
}

$$(".sekme").forEach((b) => b.addEventListener("click", () => sekmeAc(b.dataset.sekme)));

// ---- Kartlar ----

async function kartlariYukle() {
  try {
    const v = await api("GET", "/api/v1/cards");
    durum.kartlar = v.kartlar;
  } catch (err) {
    if (err.durum !== 401) bildir(hataMesaji(err), "hata");
    return;
  }
  const toplam = durum.kartlar.reduce((t, k) => t + Math.round(Number(k.bakiye) * 100), 0);
  $("#ozetBakiye").textContent = para(toplam / 100);
  $("#ozetKart").textContent = String(durum.kartlar.length);

  const secim = $("#odemeFormu").kart_id;
  const onceki = secim.value;
  secim.replaceChildren(
    ...(durum.kartlar.length
      ? durum.kartlar.map((k) => h("option", { value: k.id },
          `${k.kart_no} · ${para(k.bakiye)}${k.sahip_eposta && oturum.admin ? " · " + k.sahip_eposta : ""}`))
      : [h("option", { value: "" }, "Tanımlı kartınız yok")]),
  );
  if (onceki && durum.kartlar.some((k) => String(k.id) === onceki)) secim.value = onceki;

  const liste = $("#kartListesi");
  liste.replaceChildren(
    ...(durum.kartlar.length
      ? durum.kartlar.map((k) =>
          h("article", { class: "odeme-karti" },
            h("div", { class: "cip", "aria-hidden": "true" }),
            h("div", { class: "no" }, k.kart_no),
            h("div", { class: "alt" },
              h("div", {}, h("div", { class: "etiket" }, "Bakiye"), h("div", { class: "bakiye" }, para(k.bakiye))),
              k.sahip_eposta ? h("div", { class: "sahip" }, k.sahip_eposta) : null)))
      : [h("p", { class: "bos-kutu" }, oturum.admin
          ? "Henüz kart yok. Yönetim sekmesinden kart tanımlayabilirsiniz."
          : "Size tanımlı bir kart yok. Yöneticinizden kart tanımlamasını isteyin.")]),
  );
}

// ---- Ödemeler ----

function odemeSatiri(o) {
  const iadeEdilebilir = Number(o.iade_edilebilir) > 0;
  return h("tr", { "data-id": o.id },
    h("td", { class: "tarih", title: o.id }, tarih(o.olusturma)),
    h("td", { class: "mono" }, o.kart_no),
    h("td", { class: "aciklama gizle-mobil", title: o.aciklama || null }, o.aciklama || h("span", { class: "soluk" }, "—")),
    h("td", { class: "sag tutar" }, para(o.tutar),
      Number(o.iade_edilen) > 0 ? h("small", {}, "İade: " + para(o.iade_edilen)) : null),
    h("td", {}, h("span", { class: "rozet " + o.durum }, DURUMLAR[o.durum] || o.durum)),
    h("td", { class: "sag" }, iadeEdilebilir
      ? h("button", { type: "button", class: "btn btn-hayalet btn-kucuk", onclick: () => iadePenceresiAc(o) }, "İade")
      : null),
  );
}

async function odemeleriYukle(dahaFazla = false) {
  const tablo = $("#odemeTablosu");
  const dugme = $("#dahaFazlaBtn");
  if (!dahaFazla) {
    durum.imlec = null;
    tablo.replaceChildren(h("tr", {}, h("td", { colspan: 6, class: "bos" }, "Yükleniyor…")));
  }
  dugme.disabled = true;
  try {
    const q = new URLSearchParams({ limit: "15" });
    if (dahaFazla && durum.imlec) q.set("once", durum.imlec);
    const v = await api("GET", "/api/v1/payments?" + q);
    if (!dahaFazla) tablo.replaceChildren();
    tablo.append(...v.odemeler.map(odemeSatiri));
    if (!tablo.children.length) {
      tablo.append(h("tr", {}, h("td", { colspan: 6, class: "bos" }, "Henüz işlem yok. İlk ödemenizi soldaki formdan yapabilirsiniz.")));
    }
    durum.imlec = v.sonraki_imlec || null;
    dugme.hidden = !durum.imlec;
  } catch (err) {
    if (err.durum === 401) return;
    if (!dahaFazla) tablo.replaceChildren(h("tr", {}, h("td", { colspan: 6, class: "bos" }, "İşlemler yüklenemedi.")));
    bildir(hataMesaji(err), "hata");
  } finally {
    dugme.disabled = false;
  }
}

$("#odemeYenileBtn").addEventListener("click", () => { odemeleriYukle(); kartlariYukle(); });
$("#dahaFazlaBtn").addEventListener("click", () => odemeleriYukle(true));

// Aynı form içeriği için aynı Idempotency-Key kullanılır: ağ hatasından sonra
// "tekrar dene" çift ödeme oluşturmaz. İçerik değişince yeni anahtar üretilir.
let odemeAnahtari = null;
$("#odemeFormu").addEventListener("input", () => { odemeAnahtari = null; });

$("#odemeFormu").addEventListener("submit", (e) => {
  e.preventDefault();
  const form = e.currentTarget;
  formHatasi(form);
  form.tutar.removeAttribute("aria-invalid");

  const kartId = Number(form.kart_id.value);
  const tutar = tutarCevir(form.tutar.value);
  if (!kartId) return formHatasi(form, "Önce bir kart seçin.");
  if (!tutar || Number(tutar) <= 0) {
    form.tutar.setAttribute("aria-invalid", "true");
    form.tutar.focus();
    return formHatasi(form, "Geçerli bir tutar girin (ör. 250,50).");
  }

  odemeAnahtari ??= crypto.randomUUID();
  mesgul(form, async () => {
    try {
      const v = await api("POST", "/api/v1/payments",
        { kart_id: kartId, tutar, aciklama: form.aciklama.value.trim() || undefined },
        { idempotencyKey: odemeAnahtari });
      odemeAnahtari = null;
      bildir(`${para(v.odeme.tutar)} ödeme alındı. Kalan bakiye: ${para(v.kalan_bakiye)}`);
      form.tutar.value = "";
      form.aciklama.value = "";
    } catch (err) {
      if (err instanceof ApiHatasi) odemeAnahtari = null; // kesin yanıt geldi; yeni deneme yeni ödemedir
      if (err.kod === "yetersiz_bakiye") {
        formHatasi(form, `Yetersiz bakiye. Kartta ${para(err.veri.kalan_bakiye)} var.`);
      } else if (err.durum !== 401) {
        formHatasi(form, hataMesaji(err));
      }
    }
    kartlariYukle();
    odemeleriYukle();
  });
});

// ---- İade ----

const iadePenceresi = $("#iadePenceresi");
let iadeAnahtari = null;

function iadePenceresiAc(o) {
  durum.iadeOdemesi = o;
  iadeAnahtari = null;
  const form = $("#iadeFormu");
  formHatasi(form);
  $("#iadeBilgi").textContent =
    `${o.kart_no} kartından alınan ${para(o.tutar)} tutarındaki ödeme. İade edilebilir: ${para(o.iade_edilebilir)}.`;
  form.tutar.value = Number(o.iade_edilebilir).toFixed(2).replace(".", ",");
  iadePenceresi.showModal();
  form.tutar.select();
}

$("[data-kapat]", iadePenceresi).addEventListener("click", () => iadePenceresi.close());
$("#iadeFormu").addEventListener("input", () => { iadeAnahtari = null; });

$("#iadeFormu").addEventListener("submit", (e) => {
  e.preventDefault();
  const form = e.currentTarget;
  const o = durum.iadeOdemesi;
  formHatasi(form);
  const tutar = tutarCevir(form.tutar.value);
  if (!tutar || Number(tutar) <= 0) return formHatasi(form, "Geçerli bir tutar girin.");
  if (Number(tutar) > Number(o.iade_edilebilir)) {
    return formHatasi(form, `En fazla ${para(o.iade_edilebilir)} iade edilebilir.`);
  }

  iadeAnahtari ??= crypto.randomUUID();
  mesgul(form, async () => {
    try {
      const v = await api("POST", `/api/v1/payments/${encodeURIComponent(o.id)}/refunds`, { tutar },
        { idempotencyKey: iadeAnahtari });
      iadePenceresi.close();
      bildir(`${para(tutar)} iade edildi. Kart bakiyesi: ${para(v.kalan_bakiye)}`);
      kartlariYukle();
      odemeleriYukle();
      if (oturum.admin) mutabakatYukle();
    } catch (err) {
      if (err instanceof ApiHatasi) iadeAnahtari = null;
      if (err.durum !== 401) formHatasi(form, hataMesaji(err));
    }
  });
});

// ---- Yönetim ----

async function mutabakatYukle() {
  try {
    const r = await api("GET", "/api/v1/admin/reconciliation");
    $("#ozetIsyeri").textContent = para(r.isyeri_bakiyesi);
    const el = $("#ozetMutabakat");
    el.textContent = r.tutarli ? "Tutarlı" : `${r.uyusmayan_kartlar.length} uyuşmazlık`;
    el.className = "ozet-deger " + (r.tutarli ? "iyi" : "kotu");
    el.title = r.tutarli ? "Defter dengede ve tüm kart bakiyeleri defterle uyuşuyor." : r.uyusmayan_kartlar.join("\n");
  } catch (err) {
    if (err.durum !== 401) bildir(hataMesaji(err), "hata");
  }
}

async function kullanicilariYukle() {
  try {
    const v = await api("GET", "/api/v1/users");
    $("#kullaniciTablosu").replaceChildren(...v.kullanicilar.map((k) =>
      h("tr", {},
        h("td", {}, k.eposta),
        h("td", {}, h("span", { class: "rozet" + (k.rol === "admin" ? " admin" : "") }, k.rol === "admin" ? "Admin" : "Kullanıcı")),
        h("td", { class: "tarih" }, tarih(k.olusturma)))));
    $("#kartFormu").sahip_eposta.replaceChildren(
      h("option", { value: "" }, "— Sahipsiz (yalnızca admin kullanabilir) —"),
      ...v.kullanicilar.map((k) => h("option", { value: k.eposta }, k.eposta)),
    );
  } catch (err) {
    if (err.durum !== 401) bildir(hataMesaji(err), "hata");
  }
}

$("#kullaniciFormu").addEventListener("submit", (e) => {
  e.preventDefault();
  const form = e.currentTarget;
  formHatasi(form);
  mesgul(form, async () => {
    try {
      const k = await api("POST", "/api/v1/users", {
        eposta: form.eposta.value.trim(), sifre: form.sifre.value, rol: form.rol.value,
      });
      bildir(`${k.eposta} oluşturuldu.`);
      form.reset();
      kullanicilariYukle();
    } catch (err) {
      if (err.durum !== 401) formHatasi(form, hataMesaji(err));
    }
  });
});

async function kartlariGonder(form, kartlar) {
  const v = await api("POST", "/api/v1/cards/batch", kartlar);
  bildir(`${v.eklenen_sayi} kart eklendi` + (v.atlanan_sayi ? `, ${v.atlanan_sayi} kart zaten vardı.` : "."));
  kartlariYukle();
  mutabakatYukle();
  return v;
}

$("#kartFormu").addEventListener("submit", (e) => {
  e.preventDefault();
  const form = e.currentTarget;
  formHatasi(form);
  const bakiye = form.bakiye.value.trim() ? tutarCevir(form.bakiye.value) : "0";
  if (bakiye === null) return formHatasi(form, "Geçerli bir bakiye girin (ör. 1.500,00).");
  mesgul(form, async () => {
    try {
      const v = await kartlariGonder(form, [{
        kart_no: form.kart_no.value.trim(), bakiye, sahip_eposta: form.sahip_eposta.value || undefined,
      }]);
      if (v.eklenen_sayi) { form.kart_no.value = ""; form.bakiye.value = ""; }
    } catch (err) {
      if (err.durum !== 401) formHatasi(form, hataMesaji(err));
    }
  });
});

$("#topluBtn").addEventListener("click", async () => {
  const form = $("#kartFormu");
  formHatasi(form);
  let kartlar;
  try {
    kartlar = JSON.parse(form.toplu.value);
    if (!Array.isArray(kartlar)) throw new Error();
  } catch {
    return formHatasi(form, "JSON bir dizi olmalı: [{\"kart_no\":\"...\",\"bakiye\":0}]");
  }
  for (const k of kartlar) if (k.sahip_eposta === "") delete k.sahip_eposta;
  const dugme = $("#topluBtn");
  dugme.disabled = true;
  try {
    await kartlariGonder(form, kartlar);
  } catch (err) {
    if (err.durum !== 401) formHatasi(form, hataMesaji(err));
  } finally {
    dugme.disabled = false;
  }
});

// Kullanıcı formu düzeltmeye başlayınca eski hata mesajı kaldırılır.
$$("form").forEach((f) => f.addEventListener("input", () => formHatasi(f)));

// ---- Başlangıç ----

if (oturum.token && oturum.kullanici) {
  uygulamayiAc();
} else {
  $("#girisEkrani").hidden = false;
}
