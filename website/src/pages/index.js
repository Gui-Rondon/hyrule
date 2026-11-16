import clsx from 'clsx';
import Translate, {translate} from '@docusaurus/Translate';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import CodeBlock from '@theme/CodeBlock';
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
        <BackendExamples />
      </div>
    </header>
  );
}

const resourceExample = (backend) => `resources:
  - name: tags
    collection: tags
    backend: ${backend}
    resource_methods: [get, post]
    item_methods: [get, patch]
    schema:
      name: {type: string, required: true}
      slug: {type: string, required: true, unique: true}`;

function BackendExamples() {
  return (
    <section aria-labelledby="backend-examples">
      <Heading as="h2" id={'backend-examples'}>
        <Translate id="homepage.backends.title">One resource. Choose your backend.</Translate>
      </Heading>
      <p>
        <Translate id="homepage.backends.intro">
          Describe your API in YAML, configure the connection, and run hyrule-serve.
        </Translate>
      </p>
      <div className={styles.backendGrid}>
        <article className={styles.backendExample}>
          <Heading as="h3">MongoDB</Heading>
          <p><Translate id="homepage.backends.mongo.description">Collections are created on first write. Mongo is the default backend.</Translate></p>
          <CodeBlock language="yaml" title="resources-mongo.yaml">{resourceExample('mongo')}</CodeBlock>
          <CodeBlock language="bash">{`BACKEND=mongo CONFIG_FILE=config-mongo.json \\
RESOURCES_FILE=resources-mongo.yaml PORT=8080 \\
go run ./cmd/hyrule-serve`}</CodeBlock>
          <Link className="button button--secondary" to="/docs/reference/yaml-backends#mongo">
            <Translate id="homepage.backends.mongo.cta">Configure Mongo →</Translate>
          </Link>
        </article>
        <article className={styles.backendExample}>
          <Heading as="h3">Postgres</Heading>
          <p><Translate id="homepage.backends.postgres.description">Use a JSONB table with UUIDs. Apply the table migration before starting.</Translate></p>
          <CodeBlock language="yaml" title="resources-postgres.yaml">{resourceExample('postgres')}</CodeBlock>
          <CodeBlock language="bash">{`BACKEND=postgres CONFIG_FILE=config-postgres.json \\
RESOURCES_FILE=resources-postgres.yaml PORT=8080 \\
go run ./cmd/hyrule-serve`}</CodeBlock>
          <Link className="button button--secondary" to="/docs/reference/yaml-backends#postgres">
            <Translate id="homepage.backends.postgres.cta">Configure Postgres →</Translate>
          </Link>
        </article>
      </div>
    </section>
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
