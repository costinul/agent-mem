import os
from dotenv import load_dotenv
load_dotenv(os.path.join(os.path.dirname(__file__), '..', '.env'))
import psycopg2

conn = psycopg2.connect(os.environ["POSTGRES_DSN"])
conn.autocommit = True
cur = conn.cursor()
cur.execute('ALTER DATABASE "agent-mem" SET ivfflat.probes = 10')
print('set ivfflat.probes = 10 for database')
cur.execute('SHOW ivfflat.probes')
print('current session value:', cur.fetchone())
conn.close()
