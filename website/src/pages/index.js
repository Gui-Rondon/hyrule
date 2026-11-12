import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useBaseUrl from '@docusaurus/useBaseUrl';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import styles from './index.module.css';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <span className={styles.diamondLeft} aria-hidden="true" />
      <span className={styles.diamondCenter} aria-hidden="true" />
      <span className={styles.diamondRight} aria-hidden="true" />
      <div className="container">
        <img
          src={useBaseUrl('/img/triforce.png')}
          alt=""
          className={styles.triforce}
        />
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className="hero__subtitle">{siteConfig.tagline}</p>
        <div className={styles.buttons}>
          <Link className="button button--secondary button--lg" to="/docs/learn/intro">
            Start Learning
          </Link>
          <Link
            className={clsx('button button--outline button--lg', styles.secondaryButton)}
            to="/docs/reference/architecture">
            Reference
          </Link>
        </div>
        <pre className={styles.codeTeaser}>
          <code>{`resources:
  - name: tags
    collection: tags
    schema:
      name:
        type: string
        required: true
      slug:
        type: string
        required: true
        unique: true`}</code>
        </pre>
      </div>
    </header>
  );
}

const cards = [
  {
    title: 'Learn',
    description:
      'A guided, step-by-step path: build a resource, add relations and hooks, wire up a transactional outbox, and test all of it — using one real example throughout.',
    to: '/docs/learn/intro',
    cta: 'Start the guide →',
  },
  {
    title: 'Reference',
    description:
      'The complete, example-driven detail: the Store interface, the resources.yaml field reference, the outbox, dry run, testkit, and the architecture that holds it together.',
    to: '/docs/reference/architecture',
    cta: 'Browse the reference →',
  },
  {
    title: 'Blog',
    description:
      "Guilherme Rondon's writing on Go, infrastructure and the ideas behind hyrule and his other projects.",
    to: 'https://guirondon.dev/posts/',
    cta: 'Read the blog →',
  },
];

function HomepageCards() {
  return (
    <section className={styles.cards}>
      <div className="container">
        <div className="row">
          {cards.map((card) => (
            <div key={card.title} className="col col--4">
              <div className={styles.card}>
                <Heading as="h2">{card.title}</Heading>
                <p>{card.description}</p>
                <Link to={card.to}>{card.cta}</Link>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

export default function Home() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={siteConfig.title}
      description="A Go framework for scaffolding microservices fast: describe a resource in YAML and get a working REST API — CRUD, validation, ETags, a transactional outbox — with a testkit built in so you can test its flows without standing up the whole stack.">
      <HomepageHeader />
      <hr className="hy-divider" />
      <main>
        <HomepageCards />
      </main>
    </Layout>
  );
}
