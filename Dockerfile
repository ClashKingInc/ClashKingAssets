FROM python:3.11-bookworm

RUN apt-get update && apt-get install -y libsnappy-dev

COPY requirements.txt /app/
WORKDIR /app

RUN pip install -r requirements.txt
COPY . .

EXPOSE 6000

CMD ["gunicorn", "-w", "2", "-k", "uvicorn.workers.UvicornWorker", "main:app", "--bind", "0.0.0.0:8000"]

