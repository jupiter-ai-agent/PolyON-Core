// Package template provides embedded Next.js template files
// for initialising new Gitea repos.
package template

import "encoding/base64"

// LandingTemplateFiles returns a map of filePath → base64-encoded content
// for the landing page template.
func LandingTemplateFiles() map[string]string {
	files := map[string]string{
		"package.json":         landingPackageJSON,
		"next.config.js":       landingNextConfigJS,
		".gitignore":           landingGitignore,
		"pages/index.js":       landingPagesIndex,
		"pages/_app.js":        landingPagesApp,
		"styles/globals.css":   landingStylesGlobal,
		"lib/strapi.js":        landingLibStrapi,
	}

	result := make(map[string]string, len(files))
	for path, content := range files {
		result[path] = base64.StdEncoding.EncodeToString([]byte(content))
	}
	return result
}

const landingPackageJSON = `{
  "name": "polyon-landing",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start",
    "lint": "next lint"
  },
  "dependencies": {
    "next": "14.2.3",
    "react": "^18.3.1",
    "react-dom": "^18.3.1"
  },
  "devDependencies": {
    "eslint": "^8",
    "eslint-config-next": "14.2.3"
  }
}
`

const landingNextConfigJS = `/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'export',
  trailingSlash: true,
  images: {
    unoptimized: true,
  },
  env: {
    NEXT_PUBLIC_STRAPI_URL: process.env.NEXT_PUBLIC_STRAPI_URL || '',
  },
}

module.exports = nextConfig
`

const landingGitignore = `node_modules/
.next/
out/
.env*.local
.DS_Store
`

const landingPagesApp = `import '../styles/globals.css'

export default function App({ Component, pageProps }) {
  return <Component {...pageProps} />
}
`

const landingPagesIndex = `import Head from 'next/head'
import { fetchPage } from '../lib/strapi'

export default function Landing({ page }) {
  const title = page?.title || 'HELIOS Landing'
  const tagline = page?.tagline || '제품명을 한 줄로 설명하세요'
  const description = page?.description || '고객의 문제를 어떻게 해결하는지 간결하게 씁니다.'
  const ctaLabel = page?.ctaLabel || '무료로 시작하기'
  const ctaHref = page?.ctaHref || '#pricing'
  const features = page?.features || [
    { icon: '⚡', title: '빠름', desc: '로딩 없이 즉시 시작합니다.' },
    { icon: '🔒', title: '안전함', desc: '엔터프라이즈급 보안을 갖췄습니다.' },
    { icon: '📈', title: '확장 가능', desc: '팀이 성장해도 함께 확장됩니다.' },
    { icon: '🤝', title: '협업', desc: '팀원 모두가 실시간으로 함께 합니다.' },
    { icon: '🛠', title: '통합', desc: '즐겨 쓰는 도구와 바로 연결됩니다.' },
    { icon: '💡', title: '스마트', desc: 'AI 인사이트로 의사결정을 돕습니다.' },
  ]
  const plans = page?.plans || [
    { name: '스타터', price: '0', period: '/월', desc: '소규모 팀에 최적', features: ['최대 3명', '기본 기능', '이메일 지원'], cta: '무료 시작', highlight: false },
    { name: '프로', price: '49,000', period: '/월', desc: '성장하는 팀을 위해', features: ['최대 20명', '모든 기능', '우선 지원', 'API 접근'], cta: '14일 무료 체험', highlight: true },
    { name: '엔터프라이즈', price: '문의', period: '', desc: '대규모 조직', features: ['무제한', 'SLA 보장', '전담 관리자', 'SSO/SAML'], cta: '영업팀 연락', highlight: false },
  ]
  const faqs = page?.faqs || [
    { q: '무료 체험 기간 후 자동 결제되나요?', a: '아니요. 체험 종료 후 요금이 청구되지 않습니다. 업그레이드는 언제든지 선택할 수 있습니다.' },
    { q: '언제든지 해지할 수 있나요?', a: '네, 언제든지 해지할 수 있으며 데이터도 내보낼 수 있습니다.' },
    { q: '카드 없이 체험할 수 있나요?', a: '네, 스타터 플랜은 신용카드 없이 시작할 수 있습니다.' },
    { q: '팀 규모가 늘면 어떻게 하나요?', a: '언제든지 플랜을 업그레이드하거나 시트를 추가할 수 있습니다.' },
  ]

  return (
    <>
      <Head>
        <title>{title}</title>
        <meta name="description" content={description} />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <link rel="icon" href="/favicon.ico" />
        <meta property="og:title" content={title} />
        <meta property="og:description" content={description} />
      </Head>

      {/* ── Nav ── */}
      <nav className="lp-nav">
        <div className="lp-container lp-nav-inner">
          <span className="lp-logo">◆ {title}</span>
          <div className="lp-nav-links">
            <a href="#features">기능</a>
            <a href="#pricing">가격</a>
            <a href="#faq">FAQ</a>
          </div>
          <a href={ctaHref} className="lp-btn lp-btn-sm">{ctaLabel}</a>
        </div>
      </nav>

      {/* ── Hero ── */}
      <section className="lp-hero">
        <div className="lp-container lp-hero-inner">
          <div className="lp-social-proof">
            ⭐⭐⭐⭐⭐ &nbsp;<strong>4.9/5</strong>&nbsp; · 고객 1,200+ 명의 신뢰
          </div>
          <h1 className="lp-hero-h1">{tagline}</h1>
          <p className="lp-hero-desc">{description}</p>
          <div className="lp-hero-ctas">
            <a href={ctaHref} className="lp-btn lp-btn-xl">{ctaLabel} →</a>
            <a href="#features" className="lp-btn lp-btn-xl lp-btn-ghost">기능 살펴보기</a>
          </div>
          <p className="lp-hero-note">신용카드 필요 없음 · 14일 무료 체험</p>
        </div>
      </section>

      {/* ── Features ── */}
      <section id="features" className="lp-section lp-section-alt">
        <div className="lp-container">
          <div className="lp-section-header">
            <span className="lp-tag">Features</span>
            <h2>왜 선택해야 할까요?</h2>
            <p>고객이 가장 아끼는 기능들을 한눈에 확인하세요.</p>
          </div>
          <div className="lp-features-grid">
            {features.map((f, i) => (
              <div key={i} className="lp-feature-card">
                <span className="lp-feature-icon">{f.icon}</span>
                <h3>{f.title}</h3>
                <p>{f.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── Pricing ── */}
      <section id="pricing" className="lp-section">
        <div className="lp-container">
          <div className="lp-section-header">
            <span className="lp-tag">Pricing</span>
            <h2>투명한 가격 정책</h2>
            <p>숨은 비용 없이 팀에 맞는 플랜을 선택하세요.</p>
          </div>
          <div className="lp-plans-grid">
            {plans.map((plan, i) => (
              <div key={i} className={'lp-plan-card' + (plan.highlight ? ' lp-plan-highlight' : '')}>
                {plan.highlight && <div className="lp-plan-badge">인기</div>}
                <div className="lp-plan-name">{plan.name}</div>
                <div className="lp-plan-price">
                  <span className="lp-plan-amount">{plan.price}</span>
                  {plan.price !== '문의' && <span className="lp-plan-currency">원</span>}
                  <span className="lp-plan-period">{plan.period}</span>
                </div>
                <p className="lp-plan-desc">{plan.desc}</p>
                <ul className="lp-plan-features">
                  {plan.features.map((feat, j) => (
                    <li key={j}>✓ {feat}</li>
                  ))}
                </ul>
                <a href={ctaHref} className={'lp-btn lp-btn-block' + (plan.highlight ? '' : ' lp-btn-outline-dark')}>
                  {plan.cta}
                </a>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* ── FAQ ── */}
      <section id="faq" className="lp-section lp-section-alt">
        <div className="lp-container lp-faq-wrap">
          <div className="lp-section-header">
            <span className="lp-tag">FAQ</span>
            <h2>자주 묻는 질문</h2>
          </div>
          <div className="lp-faq-list">
            {faqs.map((item, i) => (
              <details key={i} className="lp-faq-item">
                <summary className="lp-faq-q">{item.q}</summary>
                <p className="lp-faq-a">{item.a}</p>
              </details>
            ))}
          </div>
        </div>
      </section>

      {/* ── Footer CTA ── */}
      <section className="lp-footer-cta">
        <div className="lp-container lp-footer-cta-inner">
          <h2>지금 시작하세요</h2>
          <p>수천 명의 고객이 선택한 솔루션을 경험하세요.</p>
          <a href={ctaHref} className="lp-btn lp-btn-xl lp-btn-white">{ctaLabel} →</a>
        </div>
      </section>

      {/* ── Footer ── */}
      <footer className="lp-footer">
        <div className="lp-container lp-footer-inner">
          <span className="lp-logo">◆ {title}</span>
          <p>© {new Date().getFullYear()} {title}. All rights reserved.</p>
        </div>
      </footer>
    </>
  )
}

export async function getStaticProps() {
  const page = await fetchPage('landing')
  return { props: { page: page || null } }
}
`

const landingStylesGlobal = `/* PolyON Landing Template — globals.css */
@import url('https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:ital,wght@0,300;0,400;0,500;0,600;0,700;1,400&display=swap');

*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

:root {
  --primary: #0f62fe;
  --primary-dark: #0043ce;
  --text: #161616;
  --text-secondary: #525252;
  --border: #e0e0e0;
  --bg: #ffffff;
  --bg-alt: #f4f4f4;
  --radius: 8px;
}

html { scroll-behavior: smooth; }
body {
  font-family: 'IBM Plex Sans', -apple-system, BlinkMacSystemFont, sans-serif;
  font-size: 16px;
  line-height: 1.6;
  color: var(--text);
  background: var(--bg);
  -webkit-font-smoothing: antialiased;
}
a { color: inherit; text-decoration: none; }

.lp-container { max-width: 1120px; margin: 0 auto; padding: 0 1.5rem; }

/* ── Nav ── */
.lp-nav {
  position: sticky; top: 0; z-index: 100;
  background: rgba(255,255,255,.95); backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--border);
}
.lp-nav-inner {
  display: flex; align-items: center; justify-content: space-between;
  height: 60px;
}
.lp-logo { font: 700 1.1rem 'IBM Plex Sans'; color: var(--text); }
.lp-nav-links { display: flex; gap: 2rem; }
.lp-nav-links a { font: 500 .9rem 'IBM Plex Sans'; color: var(--text-secondary); transition: color .15s; }
.lp-nav-links a:hover { color: var(--primary); }

/* ── Buttons ── */
.lp-btn {
  display: inline-flex; align-items: center; justify-content: center;
  padding: .65rem 1.5rem; border-radius: var(--radius);
  font: 600 .9rem 'IBM Plex Sans'; cursor: pointer; transition: all .2s;
  border: 2px solid transparent; background: var(--primary); color: #fff;
}
.lp-btn:hover { background: var(--primary-dark); transform: translateY(-1px); box-shadow: 0 4px 14px rgba(15,98,254,.25); }
.lp-btn-sm { padding: .5rem 1.25rem; font-size: .85rem; }
.lp-btn-xl { padding: .9rem 2rem; font-size: 1rem; }
.lp-btn-ghost { background: transparent; color: var(--text); border-color: var(--border); }
.lp-btn-ghost:hover { background: var(--bg-alt); box-shadow: none; }
.lp-btn-outline-dark { background: transparent; color: var(--text); border-color: var(--border); }
.lp-btn-outline-dark:hover { border-color: var(--primary); color: var(--primary); box-shadow: none; }
.lp-btn-white { background: #fff; color: var(--primary); }
.lp-btn-white:hover { background: #f4f4f4; box-shadow: none; }
.lp-btn-block { width: 100%; margin-top: 1.5rem; }

/* ── Hero ── */
.lp-hero {
  background: linear-gradient(135deg, #001141 0%, #0f3460 60%, #6929c4 100%);
  color: #fff; padding: 7rem 0 6rem; text-align: center;
}
.lp-hero-inner { max-width: 760px; margin: 0 auto; }
.lp-social-proof {
  display: inline-block; background: rgba(255,255,255,.12); border: 1px solid rgba(255,255,255,.2);
  border-radius: 24px; padding: .4rem 1.2rem; font: 13px 'IBM Plex Sans'; margin-bottom: 2rem;
}
.lp-hero-h1 {
  font: 700 clamp(2.5rem,6vw,4rem) 'IBM Plex Sans'; letter-spacing: -.03em;
  line-height: 1.1; margin-bottom: 1.5rem;
}
.lp-hero-desc { font: 300 clamp(1rem,2vw,1.2rem) 'IBM Plex Sans'; opacity: .85; max-width: 560px; margin: 0 auto 2.5rem; line-height: 1.7; }
.lp-hero-ctas { display: flex; gap: 1rem; justify-content: center; flex-wrap: wrap; margin-bottom: 1.25rem; }
.lp-hero-note { font: 13px 'IBM Plex Sans'; opacity: .6; }

/* ── Sections ── */
.lp-section { padding: 6rem 0; }
.lp-section-alt { background: var(--bg-alt); }
.lp-section-header { text-align: center; margin-bottom: 3.5rem; }
.lp-tag {
  display: inline-block; font: 700 .7rem 'IBM Plex Sans'; letter-spacing: .12em;
  text-transform: uppercase; color: var(--primary); margin-bottom: .75rem;
}
.lp-section-header h2 { font: 700 clamp(1.75rem,4vw,2.5rem) 'IBM Plex Sans'; letter-spacing: -.02em; margin-bottom: .75rem; }
.lp-section-header p { font: 1rem/1.7 'IBM Plex Sans'; color: var(--text-secondary); max-width: 500px; margin: 0 auto; }

/* ── Features ── */
.lp-features-grid {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 1.5rem;
}
.lp-feature-card {
  background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 2rem; transition: box-shadow .2s, transform .2s;
}
.lp-feature-card:hover { box-shadow: 0 8px 32px rgba(0,0,0,.08); transform: translateY(-2px); }
.lp-feature-icon { font-size: 2rem; display: block; margin-bottom: 1rem; }
.lp-feature-card h3 { font: 600 1.05rem 'IBM Plex Sans'; margin-bottom: .5rem; }
.lp-feature-card p { font: .9rem/1.6 'IBM Plex Sans'; color: var(--text-secondary); }

/* ── Pricing ── */
.lp-plans-grid {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1.5rem; align-items: start;
}
.lp-plan-card {
  background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
  padding: 2rem; position: relative; transition: box-shadow .2s;
}
.lp-plan-highlight {
  border-color: var(--primary); box-shadow: 0 0 0 2px rgba(15,98,254,.15);
}
.lp-plan-badge {
  position: absolute; top: -12px; left: 50%; transform: translateX(-50%);
  background: var(--primary); color: #fff; font: 700 11px 'IBM Plex Sans';
  padding: 3px 12px; border-radius: 12px; letter-spacing: .05em;
}
.lp-plan-name { font: 700 1rem 'IBM Plex Sans'; margin-bottom: .75rem; }
.lp-plan-price { display: flex; align-items: baseline; gap: .25rem; margin-bottom: .75rem; }
.lp-plan-amount { font: 700 2.5rem 'IBM Plex Sans'; color: var(--primary); letter-spacing: -.03em; }
.lp-plan-currency { font: 400 1rem 'IBM Plex Sans'; color: var(--text-secondary); }
.lp-plan-period { font: 400 .9rem 'IBM Plex Sans'; color: var(--text-secondary); }
.lp-plan-desc { font: .9rem/1.5 'IBM Plex Sans'; color: var(--text-secondary); margin-bottom: 1.25rem; }
.lp-plan-features { list-style: none; padding: 0; }
.lp-plan-features li { font: .9rem/2 'IBM Plex Sans'; color: var(--text); border-top: 1px solid var(--border); padding: .25rem 0; }
.lp-plan-features li:first-child { border-top: none; }

/* ── FAQ ── */
.lp-faq-wrap { max-width: 720px; margin: 0 auto; }
.lp-faq-list { display: flex; flex-direction: column; gap: .75rem; }
.lp-faq-item {
  background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius);
  overflow: hidden;
}
.lp-faq-q {
  font: 600 1rem 'IBM Plex Sans'; padding: 1.25rem 1.5rem; cursor: pointer;
  list-style: none; display: flex; justify-content: space-between; align-items: center;
}
.lp-faq-q::after { content: '+'; font-size: 1.25rem; color: var(--primary); }
details[open] .lp-faq-q::after { content: '−'; }
.lp-faq-a { font: .95rem/1.7 'IBM Plex Sans'; color: var(--text-secondary); padding: 0 1.5rem 1.25rem; }

/* ── Footer CTA ── */
.lp-footer-cta {
  background: linear-gradient(135deg, #001141 0%, #0f62fe 100%);
  color: #fff; text-align: center; padding: 5rem 0;
}
.lp-footer-cta-inner h2 { font: 700 clamp(1.75rem,4vw,2.5rem) 'IBM Plex Sans'; margin-bottom: .75rem; }
.lp-footer-cta-inner p { font: 300 1.1rem 'IBM Plex Sans'; opacity: .85; margin-bottom: 2rem; }

/* ── Footer ── */
.lp-footer { background: #161616; color: rgba(255,255,255,.6); padding: 2rem 0; }
.lp-footer-inner {
  display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 1rem;
}
.lp-footer .lp-logo { color: #fff; font-size: .95rem; }
.lp-footer p { font: .8rem 'IBM Plex Sans'; }

@media (max-width: 768px) {
  .lp-nav-links { display: none; }
  .lp-hero { padding: 5rem 0 4rem; }
  .lp-footer-inner { flex-direction: column; text-align: center; }
}
`

const landingLibStrapi = `/**
 * lib/strapi.js — Strapi API fetch helper (Landing template)
 */
const STRAPI_URL =
  process.env.NEXT_PUBLIC_STRAPI_URL ||
  process.env.STRAPI_URL ||
  ''

export async function fetchPage(slug) {
  if (!STRAPI_URL) return null
  try {
    const url = ` + "`" + `${STRAPI_URL}/api/pages?filters[slug][$eq]=${encodeURIComponent(slug)}&populate=*` + "`" + `
    const res = await fetch(url)
    if (!res.ok) return null
    const json = await res.json()
    const item = json?.data?.[0]
    if (!item) return null
    return flattenAttrs(item)
  } catch { return null }
}

export async function fetchCollection(type, params = {}) {
  if (!STRAPI_URL) return []
  try {
    const qs = new URLSearchParams({ populate: '*', ...params }).toString()
    const res = await fetch(` + "`" + `${STRAPI_URL}/api/${type}?${qs}` + "`" + `)
    if (!res.ok) return []
    const json = await res.json()
    return (json?.data || []).map(flattenAttrs)
  } catch { return [] }
}

function flattenAttrs(item) {
  if (!item) return null
  const { id, attributes } = item
  if (!attributes) return item
  return { id, ...flattenDeep(attributes) }
}

function flattenDeep(obj) {
  if (!obj || typeof obj !== 'object') return obj
  const out = {}
  for (const [key, val] of Object.entries(obj)) {
    if (val && typeof val === 'object' && 'data' in val) {
      const d = val.data
      if (Array.isArray(d)) out[key] = d.map(flattenAttrs)
      else if (d) out[key] = flattenAttrs(d)
      else out[key] = null
    } else { out[key] = val }
  }
  return out
}
`
