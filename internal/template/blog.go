// Package template provides embedded Next.js template files.
package template

import "encoding/base64"

// BlogTemplateFiles returns a map of filePath → base64-encoded content
// for the blog template (list + post detail).
func BlogTemplateFiles() map[string]string {
	files := map[string]string{
		"package.json":              blogPackageJSON,
		"next.config.js":            blogNextConfigJS,
		".gitignore":                blogGitignore,
		"pages/_app.js":             blogPagesApp,
		"pages/index.js":            blogPagesIndex,
		"pages/posts/[slug].js":     blogPagesPostSlug,
		"styles/globals.css":        blogStylesGlobal,
		"lib/strapi.js":             blogLibStrapi,
	}

	result := make(map[string]string, len(files))
	for path, content := range files {
		result[path] = base64.StdEncoding.EncodeToString([]byte(content))
	}
	return result
}

const blogPackageJSON = `{
  "name": "polyon-blog",
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

const blogNextConfigJS = `/** @type {import('next').NextConfig} */
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

const blogGitignore = `node_modules/
.next/
out/
.env*.local
.DS_Store
`

const blogPagesApp = `import '../styles/globals.css'

export default function App({ Component, pageProps }) {
  return <Component {...pageProps} />
}
`

const blogPagesIndex = `import Head from 'next/head'
import Link from 'next/link'
import { fetchPosts } from '../lib/strapi'

function timeAgo(dateStr) {
  if (!dateStr) return ''
  const diff = Date.now() - new Date(dateStr).getTime()
  const days = Math.floor(diff / 86400000)
  if (days === 0) return '오늘'
  if (days < 7) return days + '일 전'
  if (days < 30) return Math.floor(days / 7) + '주 전'
  return Math.floor(days / 30) + '개월 전'
}

export default function Blog({ posts, siteName }) {
  const name = siteName || 'HELIOS Blog'

  return (
    <>
      <Head>
        <title>{name}</title>
        <meta name="description" content="최신 글을 확인하세요." />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <link rel="icon" href="/favicon.ico" />
      </Head>

      <header className="blog-header">
        <div className="blog-container blog-header-inner">
          <Link href="/" className="blog-logo">◆ {name}</Link>
          <nav className="blog-nav">
            <Link href="/">Home</Link>
          </nav>
        </div>
      </header>

      <main className="blog-main">
        <div className="blog-container">
          <div className="blog-hero">
            <h1>{name}</h1>
            <p>생각을 공유합니다.</p>
          </div>

          {posts.length === 0 ? (
            <div className="blog-empty">
              <span>📝</span>
              <h3>아직 글이 없습니다</h3>
              <p>Strapi에서 첫 번째 포스트를 작성해보세요.</p>
            </div>
          ) : (
            <div className="blog-grid">
              {posts.map(post => (
                <article key={post.id} className="blog-card">
                  {post.cover?.url && (
                    <div className="blog-card-img">
                      <img src={post.cover.url} alt={post.title} />
                    </div>
                  )}
                  <div className="blog-card-body">
                    {post.category && (
                      <span className="blog-card-tag">{post.category}</span>
                    )}
                    <h2 className="blog-card-title">
                      <Link href={` + "`" + `/posts/${post.slug}/` + "`" + `}>{post.title}</Link>
                    </h2>
                    <p className="blog-card-excerpt">{post.excerpt || post.content?.slice(0, 160)}</p>
                    <div className="blog-card-meta">
                      {post.author && <span className="blog-card-author">{post.author}</span>}
                      <span className="blog-card-date">{timeAgo(post.publishedAt || post.createdAt)}</span>
                      <Link href={` + "`" + `/posts/${post.slug}/` + "`" + `} className="blog-card-read">읽기 →</Link>
                    </div>
                  </div>
                </article>
              ))}
            </div>
          )}
        </div>
      </main>

      <footer className="blog-footer">
        <div className="blog-container blog-footer-inner">
          <span className="blog-logo">◆ {name}</span>
          <p>© {new Date().getFullYear()} {name}. Powered by HELIOS.</p>
        </div>
      </footer>
    </>
  )
}

export async function getStaticProps() {
  const posts = await fetchPosts()
  return { props: { posts } }
}
`

const blogPagesPostSlug = `import Head from 'next/head'
import Link from 'next/link'
import { fetchPosts, fetchPostBySlug } from '../../lib/strapi'

export default function Post({ post }) {
  if (!post) {
    return (
      <div className="blog-container" style={{padding:'4rem 0',textAlign:'center'}}>
        <h1>포스트를 찾을 수 없습니다</h1>
        <Link href="/">← 돌아가기</Link>
      </div>
    )
  }

  return (
    <>
      <Head>
        <title>{post.title}</title>
        <meta name="description" content={post.excerpt || post.title} />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
      </Head>

      <header className="blog-header">
        <div className="blog-container blog-header-inner">
          <Link href="/" className="blog-logo">◆ Blog</Link>
          <nav className="blog-nav">
            <Link href="/">← 목록</Link>
          </nav>
        </div>
      </header>

      <main className="blog-main">
        <div className="blog-container blog-post-wrap">
          {post.category && <span className="blog-card-tag">{post.category}</span>}
          <h1 className="blog-post-title">{post.title}</h1>
          <div className="blog-post-meta">
            {post.author && <span>{post.author}</span>}
            {(post.publishedAt || post.createdAt) && (
              <span>{new Date(post.publishedAt || post.createdAt).toLocaleDateString('ko-KR')}</span>
            )}
          </div>
          {post.cover?.url && (
            <div className="blog-post-cover">
              <img src={post.cover.url} alt={post.title} />
            </div>
          )}
          <div className="blog-post-content">
            {(post.content || '').split('\n').map((line, i) =>
              line.trim() ? <p key={i}>{line}</p> : <br key={i} />
            )}
          </div>
          <div className="blog-post-footer">
            <Link href="/" className="blog-back-btn">← 목록으로</Link>
          </div>
        </div>
      </main>

      <footer className="blog-footer">
        <div className="blog-container blog-footer-inner">
          <span className="blog-logo">◆ Blog</span>
          <p>© {new Date().getFullYear()} PolyON Blog.</p>
        </div>
      </footer>
    </>
  )
}

export async function getStaticPaths() {
  const posts = await fetchPosts()
  const paths = posts.map(p => ({ params: { slug: p.slug || String(p.id) } }))
  return { paths, fallback: false }
}

export async function getStaticProps({ params }) {
  const post = await fetchPostBySlug(params.slug)
  return { props: { post: post || null } }
}
`

const blogStylesGlobal = `/* PolyON Blog Template — globals.css */
@import url('https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:ital,wght@0,300;0,400;0,500;0,600;0,700;1,400&family=IBM+Plex+Serif:ital,wght@0,400;0,600;1,400&display=swap');

*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

:root {
  --primary: #0f62fe;
  --text: #161616;
  --text-secondary: #525252;
  --border: #e0e0e0;
  --bg: #ffffff;
  --bg-alt: #f4f4f4;
  --radius: 6px;
}

html { scroll-behavior: smooth; }
body {
  font-family: 'IBM Plex Sans', -apple-system, BlinkMacSystemFont, sans-serif;
  font-size: 16px; line-height: 1.6;
  color: var(--text); background: var(--bg);
  -webkit-font-smoothing: antialiased;
}
a { color: inherit; text-decoration: none; }
img { max-width: 100%; display: block; object-fit: cover; }

.blog-container { max-width: 900px; margin: 0 auto; padding: 0 1.5rem; }

/* ── Header ── */
.blog-header {
  position: sticky; top: 0; z-index: 100;
  background: rgba(255,255,255,.94); backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--border);
}
.blog-header-inner {
  display: flex; align-items: center; justify-content: space-between; height: 60px;
}
.blog-logo { font: 700 1.05rem 'IBM Plex Sans'; color: var(--text); }
.blog-nav { display: flex; gap: 1.5rem; }
.blog-nav a { font: 500 .9rem 'IBM Plex Sans'; color: var(--text-secondary); transition: color .15s; }
.blog-nav a:hover { color: var(--primary); }

/* ── Main ── */
.blog-main { min-height: 70vh; padding: 3rem 0 5rem; }

.blog-hero { text-align: center; padding: 3rem 0 3.5rem; border-bottom: 1px solid var(--border); margin-bottom: 3rem; }
.blog-hero h1 { font: 700 clamp(2rem,5vw,3rem) 'IBM Plex Serif'; letter-spacing: -.02em; margin-bottom: .75rem; }
.blog-hero p { font: 300 1.1rem 'IBM Plex Sans'; color: var(--text-secondary); }

/* ── Grid ── */
.blog-grid {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 2rem;
}
.blog-card {
  border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden;
  transition: box-shadow .2s, transform .2s;
}
.blog-card:hover { box-shadow: 0 8px 28px rgba(0,0,0,.08); transform: translateY(-2px); }
.blog-card-img { height: 180px; overflow: hidden; }
.blog-card-img img { width: 100%; height: 100%; }
.blog-card-body { padding: 1.5rem; }
.blog-card-tag {
  display: inline-block; font: 700 .7rem 'IBM Plex Sans'; letter-spacing: .1em;
  text-transform: uppercase; color: var(--primary); margin-bottom: .5rem;
}
.blog-card-title { font: 600 1.1rem/1.35 'IBM Plex Serif'; margin-bottom: .75rem; }
.blog-card-title a:hover { color: var(--primary); }
.blog-card-excerpt { font: .9rem/1.6 'IBM Plex Sans'; color: var(--text-secondary); margin-bottom: 1.25rem;
  display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.blog-card-meta { display: flex; align-items: center; gap: 1rem; font: .82rem 'IBM Plex Sans'; color: #8d8d8d; }
.blog-card-author { font-weight: 600; color: var(--text-secondary); }
.blog-card-read { color: var(--primary); font-weight: 600; margin-left: auto; }
.blog-card-read:hover { text-decoration: underline; }

/* ── Empty state ── */
.blog-empty { text-align: center; padding: 5rem 1rem; }
.blog-empty span { font-size: 3rem; display: block; margin-bottom: 1rem; }
.blog-empty h3 { font: 600 1.3rem 'IBM Plex Sans'; margin-bottom: .5rem; }
.blog-empty p { font: .95rem 'IBM Plex Sans'; color: var(--text-secondary); }

/* ── Post detail ── */
.blog-post-wrap { max-width: 720px; margin: 0 auto; padding: 3rem 1.5rem; }
.blog-post-title { font: 700 clamp(1.75rem,5vw,2.75rem)/1.15 'IBM Plex Serif'; margin: .75rem 0 1rem; }
.blog-post-meta { display: flex; gap: 1.5rem; font: .85rem 'IBM Plex Sans'; color: var(--text-secondary); margin-bottom: 2rem; }
.blog-post-cover { border-radius: var(--radius); overflow: hidden; margin-bottom: 2.5rem; }
.blog-post-cover img { width: 100%; max-height: 400px; }
.blog-post-content { font: 1.05rem/1.85 'IBM Plex Serif'; }
.blog-post-content p { margin-bottom: 1.25rem; }
.blog-post-footer { margin-top: 3rem; padding-top: 2rem; border-top: 1px solid var(--border); }
.blog-back-btn {
  display: inline-flex; align-items: center; gap: .5rem;
  font: 600 .9rem 'IBM Plex Sans'; color: var(--primary);
  padding: .6rem 1.25rem; border: 1px solid var(--border); border-radius: var(--radius);
  transition: background .15s;
}
.blog-back-btn:hover { background: var(--bg-alt); }

/* ── Footer ── */
.blog-footer { background: var(--text); color: rgba(255,255,255,.6); padding: 2rem 0; }
.blog-footer-inner { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 1rem; }
.blog-footer .blog-logo { color: #fff; font-size: .95rem; }
.blog-footer p { font: .8rem 'IBM Plex Sans'; }

@media (max-width: 600px) {
  .blog-grid { grid-template-columns: 1fr; }
  .blog-footer-inner { flex-direction: column; text-align: center; }
}
`

const blogLibStrapi = `/**
 * lib/strapi.js — Strapi API fetch helper (Blog template)
 * Connects to 'posts' collection type in Strapi.
 */
const STRAPI_URL =
  process.env.NEXT_PUBLIC_STRAPI_URL ||
  process.env.STRAPI_URL ||
  ''

/** Fetch all published posts */
export async function fetchPosts() {
  if (!STRAPI_URL) return getFallbackPosts()
  try {
    const res = await fetch(` + "`" + `${STRAPI_URL}/api/posts?populate=*&sort=publishedAt:desc` + "`" + `)
    if (!res.ok) return getFallbackPosts()
    const json = await res.json()
    return (json?.data || []).map(flattenAttrs)
  } catch { return getFallbackPosts() }
}

/** Fetch a single post by slug */
export async function fetchPostBySlug(slug) {
  if (!STRAPI_URL) return null
  try {
    // Try slug first
    const res = await fetch(` + "`" + `${STRAPI_URL}/api/posts?filters[slug][$eq]=${encodeURIComponent(slug)}&populate=*` + "`" + `)
    if (res.ok) {
      const json = await res.json()
      const item = json?.data?.[0]
      if (item) return flattenAttrs(item)
    }
    // Fallback: try numeric id
    if (!isNaN(Number(slug))) {
      const res2 = await fetch(` + "`" + `${STRAPI_URL}/api/posts/${slug}?populate=*` + "`" + `)
      if (res2.ok) {
        const json2 = await res2.json()
        if (json2?.data) return flattenAttrs(json2.data)
      }
    }
    return null
  } catch { return null }
}

/** Fallback demo posts when Strapi is unavailable */
function getFallbackPosts() {
  return [
    {
      id: 1, slug: 'welcome',
      title: '첫 번째 포스트에 오신 것을 환영합니다',
      excerpt: 'HELIOS Blog 템플릿으로 블로그를 시작해보세요. Strapi CMS를 연결하면 이 데모 콘텐츠가 실제 글로 교체됩니다.',
      content: 'HELIOS Blog 템플릿으로 블로그를 시작해보세요. Strapi CMS를 연결하면 이 데모 콘텐츠가 실제 글로 교체됩니다.',
      category: '공지',
      author: 'HELIOS',
      publishedAt: new Date().toISOString(),
    },
    {
      id: 2, slug: 'getting-started',
      title: 'Strapi CMS 연동하기',
      excerpt: 'NEXT_PUBLIC_STRAPI_URL 환경변수를 설정하면 Strapi의 posts 컬렉션 타입과 자동으로 연동됩니다.',
      content: 'NEXT_PUBLIC_STRAPI_URL 환경변수를 설정하면 Strapi의 posts 컬렉션 타입과 자동으로 연동됩니다.',
      category: '튜토리얼',
      author: 'HELIOS',
      publishedAt: new Date(Date.now() - 86400000).toISOString(),
    },
  ]
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
