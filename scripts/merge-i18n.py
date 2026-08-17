#!/usr/bin/env python3
"""Merge local://i18n-*.json fragments + seeds into the two Go catalogs.

Run from the repo root. Fragment dir is the session local dir; areas are the
canonical set. en values must be verbatim template strings; es natural.
"""
import json, os, sys

FRAGDIR = os.path.expanduser("~/.omp/agent/sessions/-Projects-gogogadget/2026-08-16T16-49-35-599Z_01a00b7a-8b6f-7000-ae74-44a7165f9f0b/local")
AREAS = ["nav","sidebar","layouts","home","settings","projects","activity","admin",
         "api_tokens","billing","components","errors","blog","docs","dashboard",
         "emails","files","notifications","webhooks","flags","impersonation"]

SEEDS_EN = {
    "i18n.language": "Language",
    "email.welcome.subject": "Welcome to GoGoGadget",
    "email.payment_failed.subject": "Your payment failed",
    "email.canceled.subject": "Your subscription is canceled",
    "email.trial_ending.subject": "Your trial ends soon",
}
SEEDS_ES = {
    "i18n.language": "Idioma",
    "email.welcome.subject": "Te damos la bienvenida a GoGoGadget",
    "email.payment_failed.subject": "Tu pago falló",
    "email.canceled.subject": "Tu suscripción está cancelada",
    "email.trial_ending.subject": "Tu prueba termina pronto",
}
LEGAL = {
    "legal.terms.title": ("Terms of Service", "Términos del servicio"),
    "legal.terms.placeholder": ("Placeholder terms. Replace with counsel-reviewed copy before launch.", "Términos provisionales. Sustitúyelos por un texto revisado por un abogado antes del lanzamiento."),
    "legal.terms.body": ('GoGoGadget is provided "as is", without warranty of any kind. By using the service you agree to use it lawfully and not abuse the platform.', 'GoGoGadget se ofrece "tal cual", sin garantía de ningún tipo. Al usar el servicio aceptas usarlo de manera lícita y no abusar de la plataforma.'),
    "legal.privacy.title": ("Privacy Policy", "Política de privacidad"),
    "legal.privacy.placeholder": ("Placeholder privacy policy. Replace with counsel-reviewed copy before launch.", "Política de privacidad provisional. Sustitúyela por un texto revisado por un abogado antes del lanzamiento."),
    "legal.privacy.body": ("Account data is stored with Clerk; payment data with Polar (merchant of record); analytics, when enabled, with PostHog behind an explicit consent gate.", "Los datos de cuenta se guardan en Clerk; los datos de pago en Polar (registrante comercial); las analíticas, si están activadas, en PostHog tras un consentimiento explícito."),
}

def main():
    en, es = dict(SEEDS_EN), dict(SEEDS_ES)
    for a in AREAS:
        p = os.path.join(FRAGDIR, f"i18n-{a}.json")
        if not os.path.exists(p):
            sys.exit(f"missing fragment: {p}")
        for k, v in json.load(open(p)).items():
            if k in en and en[k] != v["en"]:
                sys.exit(f"CONFLICT on {k}: {en[k]!r} vs {v['en']!r}")
            en[k], es[k] = v["en"], v["es"]
    for k, (e, s) in LEGAL.items():
        en[k], es[k] = e, s
    assert set(en) == set(es), "en/es key mismatch"

    order = sorted(en)
    def gq(s): return json.dumps(s, ensure_ascii=False)
    def write(path, tag, comment, m):
        lines = ["package i18n", "", "import (", '\t"golang.org/x/text/language"', '\t"golang.org/x/text/message"', ")", "", comment, "func init() {"]
        for k in order:
            lines.append(f'\tmessage.SetString(language.{tag}, {gq(k)}, {gq(m[k])})')
        lines.append("}")
        open(path, "w").write("\n".join(lines) + "\n")
    write("internal/i18n/catalog_en.go", "English",
          "// English catalog: values are the exact former template strings, moved\n// verbatim. Keys are namespaced \"<area>.<name>\" and never collide with\n// display strings. DO NOT hand-order; regenerated from area fragments.", en)
    write("internal/i18n/catalog_es.go", "Spanish",
          "// Catálogo español. Traducciones revisables; el fallback a inglés está\n// garantizado por T() cuando falte una clave.", es)
    print(f"keys: {len(en)}")

if __name__ == "__main__":
    main()
