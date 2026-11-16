import clsx from 'clsx';
import Translate, {translate} from '@docusaurus/Translate';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import styles from './index.module.css';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <div className="container">
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className="hero__subtitle">
          <Translate id="homepage.tagline">
            Spin up microservices fast. Test their flows just as fast.
          </Translate>
        </p>
        <div className={styles.buttons}>
          <Link className="button button--secondary button--lg" to="/docs/learn/intro">
            <Translate id="homepage.startLearning">Start Learning</Translate>
          </Link>
          <Link
            className={clsx('button button--outline button--lg', styles.secondaryButton)}
            to="/docs/reference/architecture">
            <Translate id="homepage.reference">Reference</Translate>
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

function getHomepageCards() {
  return [
    {
      title: translate({id: 'homepage.cards.learn.title', message: 'Learn'}),
      description:
        translate({id: 'homepage.cards.learn.description', message: 'A guided, step-by-step path: build a resource, add relations and hooks, wire up a transactional outbox, and test all of it — using one real example throughout.'}),
      to: '/docs/learn/intro',
      cta: translate({id: 'homepage.cards.learn.cta', message: 'Start the guide →'}),
    },
    {
      title: translate({id: 'homepage.cards.reference.title', message: 'Reference'}),
      description:
        translate({id: 'homepage.cards.reference.description', message: 'The complete, example-driven detail: the Store interface, the resources.yaml field reference, the outbox, dry run, testkit, and the architecture that holds it together.'}),
      to: '/docs/reference/architecture',
      cta: translate({id: 'homepage.cards.reference.cta', message: 'Browse the reference →'}),
    },
    {
      title: translate({id: 'homepage.cards.blog.title', message: 'Blog'}),
      description:
        translate({id: 'homepage.cards.blog.description', message: "Guilherme Rondon's writing on Go, infrastructure and the ideas behind hyrule and his other projects."}),
      to: 'https://guirondon.dev/posts/',
      cta: translate({id: 'homepage.cards.blog.cta', message: 'Read the blog →'}),
    },
  ];
}

function HomepageCards() {
  return (
    <section className={styles.cards}>
      <div className="container">
        <div className="row">
          {getHomepageCards().map((card) => (
            <div key={card.to} className="col col--4">
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
      description={translate({id: "homepage.description", message: "A Go framework for scaffolding microservices fast: describe a resource in YAML and get a working REST API — CRUD, validation, ETags, a transactional outbox — with a testkit built in so you can test its flows without standing up the whole stack."})}>
      <HomepageHeader />
      <hr className="hy-divider" />
      <main>
        <HomepageCards />
      </main>
    </Layout>
  );
}
