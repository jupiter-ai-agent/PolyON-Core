// Package template provides embedded Next.js corporate template files
// for initialising new strapi-method Gitea repos.
package template

import "encoding/base64"

// CorporateTemplateFiles returns a map of filePath → base64-encoded content
// ready to commit via the Gitea API.
func CorporateTemplateFiles() map[string]string {
	files := map[string]string{
		"package.json":          packageJSON,
		"next.config.js":        nextConfigJS,
		".gitignore":            gitignore,
		"pages/index.js":        pagesIndex,
		"pages/_app.js":         pagesApp,
		"components/Header.js":  compHeader,
		"components/Hero.js":    compHero,
		"components/About.js":   compAbout,
		"components/Services.js": compServices,
		"components/Contact.js": compContact,
		"components/Footer.js":  compFooter,
		"styles/globals.css":    stylesGlobal,
		"lib/strapi.js":         libStrapi,
	}

	result := make(map[string]string, len(files))
	for path, content := range files {
		result[path] = base64.StdEncoding.EncodeToString([]byte(content))
	}
	return result
}

// ── Raw file contents ────────────────────────────────────────────────────────

const packageJSON = `{
  "name": "polyon-corporate",
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

const nextConfigJS = `/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'export',
  trailingSlash: true,
  images: {
    unoptimized: true,
  },
  // Allow build even without Strapi
  env: {
    NEXT_PUBLIC_STRAPI_URL: process.env.NEXT_PUBLIC_STRAPI_URL || '',
  },
}

module.exports = nextConfig
`

const gitignore = `node_modules/
.next/
out/
.env*.local
.DS_Store
`

const pagesApp = `import '../styles/globals.css'

export default function App({ Component, pageProps }) {
  return <Component {...pageProps} />
}
`

const pagesIndex = `import Head from 'next/head'
import Header from '../components/Header'
import Hero from '../components/Hero'
import About from '../components/About'
import Services from '../components/Services'
import Contact from '../components/Contact'
import Footer from '../components/Footer'
import { fetchPage } from '../lib/strapi'

export default function Home({ page }) {
  const title = page?.title || 'HELIOS Corporate'
  const description = page?.description || 'Professional corporate website'

  return (
    <>
      <Head>
        <title>{title}</title>
        <meta name="description" content={description} />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <link rel="icon" href="/favicon.ico" />
      </Head>

      <Header nav={page?.navigation} />
      <main>
        <Hero data={page?.hero} />
        <About data={page?.about} />
        <Services data={page?.services} />
        <Contact data={page?.contact} />
      </main>
      <Footer data={page?.footer} />
    </>
  )
}

export async function getStaticProps() {
  const page = await fetchPage('homepage')
  return {
    props: { page: page || null },
  }
}
`

const compHeader = `import { useState } from 'react'
import Link from 'next/link'

const defaultNav = [
  { label: '소개', href: '#about' },
  { label: '서비스', href: '#services' },
  { label: '연락처', href: '#contact' },
]

export default function Header({ nav }) {
  const [open, setOpen] = useState(false)
  const items = nav || defaultNav

  return (
    <header className="header">
      <div className="container header-inner">
        <Link href="/" className="logo">
          <span className="logo-mark">◆</span>
          <span className="logo-name">HELIOS</span>
        </Link>

        <nav className={` + "`" + `nav ${open ? 'nav-open' : ''}` + "`" + `}>
          {items.map((item, i) => (
            <a key={i} href={item.href} className="nav-link" onClick={() => setOpen(false)}>
              {item.label}
            </a>
          ))}
        </nav>

        <button
          className="hamburger"
          onClick={() => setOpen(!open)}
          aria-label="메뉴"
        >
          <span /><span /><span />
        </button>
      </div>
    </header>
  )
}
`

const compHero = `export default function Hero({ data }) {
  const title = data?.title || '성공을 위한\n최고의 파트너'
  const subtitle = data?.subtitle || '혁신적인 기술과 전문적인 서비스로\n귀사의 비즈니스를 한 단계 높여드립니다.'
  const cta = data?.cta || { label: '서비스 알아보기', href: '#services' }
  const cta2 = data?.cta2 || { label: '연락하기', href: '#contact' }

  return (
    <section className="hero">
      <div className="hero-bg" />
      <div className="container hero-content">
        <h1 className="hero-title">{title.split('\\n').map((t, i) => (
          <span key={i}>{t}{i < title.split('\\n').length - 1 && <br />}</span>
        ))}</h1>
        <p className="hero-sub">{subtitle.split('\\n').map((t, i) => (
          <span key={i}>{t}{i < subtitle.split('\\n').length - 1 && <br />}</span>
        ))}</p>
        <div className="hero-actions">
          <a href={cta.href} className="btn btn-primary">{cta.label}</a>
          <a href={cta2.href} className="btn btn-outline">{cta2.label}</a>
        </div>
      </div>
    </section>
  )
}
`

const compAbout = `export default function About({ data }) {
  const title = data?.title || '회사 소개'
  const desc = data?.description || '저희는 고객의 성공을 최우선으로 생각하는 전문 기업입니다. 10년 이상의 경험과 전문성을 바탕으로 최고의 서비스를 제공합니다.'
  const stats = data?.stats || [
    { value: '10+', label: '년 경험' },
    { value: '500+', label: '고객사' },
    { value: '99%', label: '고객 만족도' },
    { value: '24/7', label: '지원' },
  ]

  return (
    <section id="about" className="section about">
      <div className="container">
        <div className="about-grid">
          <div className="about-text">
            <span className="section-tag">About Us</span>
            <h2 className="section-title">{title}</h2>
            <p className="section-desc">{desc}</p>
          </div>
          <div className="stats-grid">
            {stats.map((stat, i) => (
              <div key={i} className="stat-card">
                <span className="stat-value">{stat.value}</span>
                <span className="stat-label">{stat.label}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  )
}
`

const compServices = `export default function Services({ data }) {
  const title = data?.title || '서비스'
  const items = data?.items || [
    {
      icon: '🚀',
      title: '디지털 전환',
      desc: '최신 기술로 비즈니스를 디지털화하고 효율성을 극대화합니다.',
    },
    {
      icon: '⚡',
      title: '클라우드 솔루션',
      desc: '안정적이고 확장 가능한 클라우드 인프라를 구축합니다.',
    },
    {
      icon: '🔒',
      title: '보안 & 컴플라이언스',
      desc: '엔터프라이즈급 보안으로 데이터를 안전하게 보호합니다.',
    },
    {
      icon: '📊',
      title: '데이터 분석',
      desc: '데이터 기반 인사이트로 스마트한 비즈니스 의사결정을 지원합니다.',
    },
    {
      icon: '🤝',
      title: '컨설팅',
      desc: '전문가 팀이 귀사의 목표 달성을 위한 최적의 전략을 수립합니다.',
    },
    {
      icon: '💡',
      title: '혁신 R&D',
      desc: '지속적인 연구개발로 미래를 선도하는 솔루션을 제공합니다.',
    },
  ]

  return (
    <section id="services" className="section services">
      <div className="container">
        <div className="section-header">
          <span className="section-tag">Services</span>
          <h2 className="section-title">{title}</h2>
        </div>
        <div className="services-grid">
          {items.map((svc, i) => (
            <div key={i} className="service-card">
              <span className="service-icon">{svc.icon}</span>
              <h3 className="service-title">{svc.title}</h3>
              <p className="service-desc">{svc.desc}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
`

const compContact = `export default function Contact({ data }) {
  const title = data?.title || '연락처'
  const desc = data?.description || '궁금한 점이 있으시면 언제든지 연락해 주세요.'
  const email = data?.email || 'hello@example.com'
  const phone = data?.phone || '02-1234-5678'
  const address = data?.address || '서울특별시 강남구 테헤란로 123'

  return (
    <section id="contact" className="section contact">
      <div className="container">
        <div className="section-header">
          <span className="section-tag">Contact</span>
          <h2 className="section-title">{title}</h2>
          <p className="section-desc">{desc}</p>
        </div>
        <div className="contact-grid">
          <div className="contact-info">
            <div className="contact-item">
              <span className="contact-icon">📧</span>
              <div>
                <div className="contact-label">이메일</div>
                <a href={` + "`" + `mailto:${email}` + "`" + `} className="contact-value">{email}</a>
              </div>
            </div>
            <div className="contact-item">
              <span className="contact-icon">📞</span>
              <div>
                <div className="contact-label">전화</div>
                <a href={` + "`" + `tel:${phone}` + "`" + `} className="contact-value">{phone}</a>
              </div>
            </div>
            <div className="contact-item">
              <span className="contact-icon">📍</span>
              <div>
                <div className="contact-label">주소</div>
                <span className="contact-value">{address}</span>
              </div>
            </div>
          </div>
          <form className="contact-form" onSubmit={(e) => e.preventDefault()}>
            <input type="text" placeholder="이름" className="form-input" required />
            <input type="email" placeholder="이메일" className="form-input" required />
            <textarea placeholder="메시지" className="form-textarea" rows={5} required />
            <button type="submit" className="btn btn-primary">보내기</button>
          </form>
        </div>
      </div>
    </section>
  )
}
`

const compFooter = `export default function Footer({ data }) {
  const company = data?.company || 'HELIOS'
  const links = data?.links || [
    { label: '소개', href: '#about' },
    { label: '서비스', href: '#services' },
    { label: '연락처', href: '#contact' },
  ]
  const year = new Date().getFullYear()

  return (
    <footer className="footer">
      <div className="container footer-inner">
        <div className="footer-brand">
          <span className="logo-mark">◆</span>
          <span className="logo-name">{company}</span>
        </div>
        <nav className="footer-nav">
          {links.map((link, i) => (
            <a key={i} href={link.href} className="footer-link">{link.label}</a>
          ))}
        </nav>
        <p className="footer-copy">© {year} {company}. All rights reserved.</p>
      </div>
    </footer>
  )
}
`

const stylesGlobal = `/* PolyON Corporate Template — globals.css */
@import url('https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:ital,wght@0,300;0,400;0,500;0,600;0,700;1,400&display=swap');

*, *::before, *::after {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

:root {
  --primary: #0f62fe;
  --primary-dark: #0043ce;
  --secondary: #393939;
  --accent: #6929c4;
  --bg: #ffffff;
  --bg-subtle: #f4f4f4;
  --text: #161616;
  --text-secondary: #525252;
  --border: #e0e0e0;
  --radius: 8px;
  --shadow: 0 1px 3px rgba(0,0,0,.12), 0 1px 2px rgba(0,0,0,.08);
  --shadow-lg: 0 10px 40px rgba(0,0,0,.12);
}

html {
  scroll-behavior: smooth;
}

body {
  font-family: 'IBM Plex Sans', -apple-system, BlinkMacSystemFont, sans-serif;
  font-size: 16px;
  line-height: 1.6;
  color: var(--text);
  background: var(--bg);
  -webkit-font-smoothing: antialiased;
}

a { color: inherit; text-decoration: none; }
img { max-width: 100%; display: block; }

.container {
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 1.5rem;
}

/* ── Header ── */
.header {
  position: sticky;
  top: 0;
  z-index: 100;
  background: rgba(255,255,255,.92);
  backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border);
}

.header-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 64px;
}

.logo {
  display: flex;
  align-items: center;
  gap: .5rem;
  font-weight: 700;
  font-size: 1.1rem;
  color: var(--text);
}

.logo-mark { color: var(--primary); font-size: 1.2rem; }

.nav { display: flex; gap: 2rem; }
.nav-link {
  font-size: .9rem;
  font-weight: 500;
  color: var(--text-secondary);
  transition: color .2s;
}
.nav-link:hover { color: var(--primary); }

.hamburger {
  display: none;
  flex-direction: column;
  gap: 5px;
  background: none;
  border: none;
  cursor: pointer;
  padding: .5rem;
}
.hamburger span {
  display: block;
  width: 22px;
  height: 2px;
  background: var(--text);
  border-radius: 2px;
  transition: .3s;
}

@media (max-width: 768px) {
  .hamburger { display: flex; }
  .nav {
    display: none;
    position: absolute;
    top: 64px;
    left: 0;
    right: 0;
    background: var(--bg);
    flex-direction: column;
    gap: 0;
    border-bottom: 1px solid var(--border);
    padding: 1rem 1.5rem;
  }
  .nav.nav-open { display: flex; }
  .nav-link { padding: .75rem 0; border-bottom: 1px solid var(--border); }
}

/* ── Hero ── */
.hero {
  position: relative;
  min-height: 90vh;
  display: flex;
  align-items: center;
  overflow: hidden;
  background: linear-gradient(135deg, #001141 0%, #0f3460 50%, #6929c4 100%);
  color: #fff;
}

.hero-bg {
  position: absolute;
  inset: 0;
  background:
    radial-gradient(ellipse at 20% 50%, rgba(105,41,196,.4) 0%, transparent 60%),
    radial-gradient(ellipse at 80% 20%, rgba(15,98,254,.3) 0%, transparent 60%);
}

.hero-content {
  position: relative;
  z-index: 1;
  padding: 8rem 1.5rem;
}

.hero-title {
  font-size: clamp(2.5rem, 6vw, 4.5rem);
  font-weight: 700;
  line-height: 1.15;
  margin-bottom: 1.5rem;
  letter-spacing: -.02em;
}

.hero-sub {
  font-size: clamp(1rem, 2vw, 1.25rem);
  color: rgba(255,255,255,.8);
  max-width: 540px;
  margin-bottom: 2.5rem;
  font-weight: 300;
  line-height: 1.7;
}

.hero-actions { display: flex; gap: 1rem; flex-wrap: wrap; }

/* ── Buttons ── */
.btn {
  display: inline-flex;
  align-items: center;
  padding: .8rem 1.75rem;
  border-radius: var(--radius);
  font-size: .95rem;
  font-weight: 600;
  cursor: pointer;
  border: 2px solid transparent;
  transition: all .2s;
  font-family: inherit;
}

.btn-primary {
  background: var(--primary);
  color: #fff;
  border-color: var(--primary);
}
.btn-primary:hover {
  background: var(--primary-dark);
  border-color: var(--primary-dark);
  transform: translateY(-1px);
  box-shadow: 0 4px 14px rgba(15,98,254,.3);
}

.btn-outline {
  background: transparent;
  color: #fff;
  border-color: rgba(255,255,255,.5);
}
.btn-outline:hover {
  background: rgba(255,255,255,.1);
  border-color: #fff;
}

/* ── Sections ── */
.section { padding: 6rem 0; }
.section:nth-child(even) { background: var(--bg-subtle); }

.section-header { text-align: center; margin-bottom: 3.5rem; }
.section-tag {
  display: inline-block;
  font-size: .75rem;
  font-weight: 700;
  letter-spacing: .12em;
  text-transform: uppercase;
  color: var(--primary);
  margin-bottom: .75rem;
}
.section-title {
  font-size: clamp(1.75rem, 4vw, 2.75rem);
  font-weight: 700;
  letter-spacing: -.02em;
  margin-bottom: 1rem;
  color: var(--text);
}
.section-desc {
  font-size: 1.05rem;
  color: var(--text-secondary);
  max-width: 560px;
  margin: 0 auto;
  line-height: 1.7;
}

/* ── About ── */
.about-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 5rem;
  align-items: center;
}
.about-text .section-tag,
.about-text .section-title,
.about-text .section-desc {
  text-align: left;
}

.stats-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1.5rem;
}

.stat-card {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 1.75rem;
  text-align: center;
  box-shadow: var(--shadow);
  transition: box-shadow .2s, transform .2s;
}
.stat-card:hover {
  box-shadow: var(--shadow-lg);
  transform: translateY(-2px);
}
.stat-value {
  display: block;
  font-size: 2.25rem;
  font-weight: 700;
  color: var(--primary);
  letter-spacing: -.02em;
}
.stat-label {
  font-size: .85rem;
  color: var(--text-secondary);
  margin-top: .25rem;
}

@media (max-width: 768px) {
  .about-grid { grid-template-columns: 1fr; gap: 3rem; }
}

/* ── Services ── */
.services-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1.5rem;
}

.service-card {
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 2rem;
  transition: all .2s;
  cursor: default;
}
.service-card:hover {
  border-color: var(--primary);
  box-shadow: var(--shadow-lg);
  transform: translateY(-3px);
}
.service-icon { font-size: 2rem; display: block; margin-bottom: 1rem; }
.service-title { font-size: 1.1rem; font-weight: 600; margin-bottom: .5rem; }
.service-desc { font-size: .9rem; color: var(--text-secondary); line-height: 1.6; }

/* ── Contact ── */
.contact-grid {
  display: grid;
  grid-template-columns: 1fr 1.5fr;
  gap: 4rem;
  align-items: start;
}

.contact-item {
  display: flex;
  gap: 1rem;
  align-items: flex-start;
  margin-bottom: 2rem;
}
.contact-icon { font-size: 1.5rem; line-height: 1; }
.contact-label { font-size: .8rem; color: var(--text-secondary); margin-bottom: .2rem; font-weight: 500; }
.contact-value { font-size: 1rem; color: var(--text); }
a.contact-value:hover { color: var(--primary); }

.contact-form { display: flex; flex-direction: column; gap: 1rem; }
.form-input, .form-textarea {
  width: 100%;
  padding: .8rem 1rem;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  font-size: .95rem;
  font-family: inherit;
  color: var(--text);
  background: var(--bg);
  transition: border-color .2s;
  resize: vertical;
}
.form-input:focus, .form-textarea:focus {
  outline: none;
  border-color: var(--primary);
  box-shadow: 0 0 0 3px rgba(15,98,254,.1);
}

@media (max-width: 768px) {
  .contact-grid { grid-template-columns: 1fr; gap: 2.5rem; }
}

/* ── Footer ── */
.footer {
  background: var(--text);
  color: rgba(255,255,255,.7);
  padding: 2.5rem 0;
}
.footer-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 1rem;
}
.footer .logo-mark { color: var(--primary); }
.footer .logo-name { color: #fff; }
.footer-nav { display: flex; gap: 1.5rem; }
.footer-link { font-size: .85rem; color: rgba(255,255,255,.6); transition: color .2s; }
.footer-link:hover { color: #fff; }
.footer-copy { font-size: .8rem; }

@media (max-width: 768px) {
  .footer-inner { flex-direction: column; text-align: center; }
  .footer-nav { justify-content: center; }
}
`

const libStrapi = `/**
 * lib/strapi.js — Strapi API fetch helper
 *
 * Uses NEXT_PUBLIC_STRAPI_URL (build-time env) or STRAPI_URL.
 * Falls back gracefully if Strapi is unavailable.
 */

const STRAPI_URL =
  process.env.NEXT_PUBLIC_STRAPI_URL ||
  process.env.STRAPI_URL ||
  ''

/**
 * fetchPage — fetches a page by slug from Strapi Content API.
 * Returns null if Strapi is unavailable or the page doesn't exist.
 *
 * Assumes you have a "pages" collection type in Strapi with a "slug" field.
 */
export async function fetchPage(slug) {
  if (!STRAPI_URL) {
    return null
  }

  try {
    const url = ` + "`" + `${STRAPI_URL}/api/pages?filters[slug][$eq]=${encodeURIComponent(slug)}&populate=*` + "`" + `
    const res = await fetch(url, {
      headers: { 'Content-Type': 'application/json' },
      // Next.js fetch options for ISR / static generation
      next: { revalidate: 60 },
    })

    if (!res.ok) {
      console.warn(` + "`" + `[strapi] fetchPage(${slug}): HTTP ${res.status}` + "`" + `)
      return null
    }

    const json = await res.json()
    const item = json?.data?.[0]
    if (!item) return null

    // Flatten Strapi v4 attributes wrapper
    return flattenAttrs(item)
  } catch (err) {
    console.warn(` + "`" + `[strapi] fetchPage(${slug}) error:` + "`" + `, err.message)
    return null
  }
}

/**
 * fetchCollection — generic collection fetch.
 */
export async function fetchCollection(type, params = {}) {
  if (!STRAPI_URL) return []

  try {
    const qs = new URLSearchParams({ populate: '*', ...params }).toString()
    const res = await fetch(` + "`" + `${STRAPI_URL}/api/${type}?${qs}` + "`" + `)
    if (!res.ok) return []
    const json = await res.json()
    return (json?.data || []).map(flattenAttrs)
  } catch {
    return []
  }
}

/** Flatten Strapi v4 { id, attributes: {...} } → { id, ...attributes } */
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
      // Relation
      const d = val.data
      if (Array.isArray(d)) {
        out[key] = d.map(flattenAttrs)
      } else if (d) {
        out[key] = flattenAttrs(d)
      } else {
        out[key] = null
      }
    } else {
      out[key] = val
    }
  }
  return out
}
`
