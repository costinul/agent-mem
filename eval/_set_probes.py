import os
from dotenv import load_dotenv
load_dotenv(os.path.join(os.path.dirname(__file__), '..', '.env'))
import psycopg2

conn = psycopg2.connect(
    host='agent-mem.postgres.database.azure.com', port=5432, dbname='agent-mem',
    user='agentmemadm', password='6dFAW49oXbNLNU8S3gqGLJKp8FTAAB2w', sslmode='require',
)
conn.autocommit = True
cur = conn.cursor()
cur.execute('ALTER DATABASE "agent-mem" SET ivfflat.probes = 10')
print('set ivfflat.probes = 10 for database')
cur.execute('SHOW ivfflat.probes')
print('current session value:', cur.fetchone())
conn.close()
