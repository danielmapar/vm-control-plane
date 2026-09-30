FROM docker.io/library/python:3.13-slim-trixie

RUN apt-get update \
    && apt-get install -y --no-install-recommends openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir ansible-core==2.21.4

WORKDIR /ansible
COPY playbook.yml .

ENTRYPOINT ["ansible-playbook", "playbook.yml"]
