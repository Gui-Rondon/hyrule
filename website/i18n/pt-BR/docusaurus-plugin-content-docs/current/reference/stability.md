---
sidebar_position: 21
---
# Estabilidade, releases e suporte

O Hyrule é pré-v1. APIs exportadas estão disponíveis, mas isso não garante
estabilidade integral. A [política de compatibilidade](https://github.com/Gui-Rondon/hyrule/blob/main/COMPATIBILITY.md)
cobre contratos Go, HTTP, CLI e configuração, maturidade e suporte. Antes da v1,
quebras exigem nova minor; depois, nova major. Depreciações duram ao menos uma minor,
exceto correções urgentes de segurança documentadas.

Antes da v1, o suporte cobre a última minor no patch mais recente. Após a v1,
as duas últimas minors da major atual recebem correções; a minor final da major
anterior recebe seis meses de correções de segurança após uma nova major.
Go 1.26 e 1.27 são testados.

A v1 exige inventário público revisado, ausência de quebras conhecidas pendentes,
duas minors consecutivas compatíveis, integrações/matriz Go verdes e exemplos
externos reproduzíveis com migração documentada. SQLite permanece experimental.

O mantenedor publica releases manualmente, com binários Linux/Windows amd64/arm64,
checksums e proveniência GitHub/SLSA assinada. Isso não certifica um nível SLSA.
A solicitação ao proxy é automática; pkg.go.dev/deps.dev podem demorar a atualizar.
Veja [operações de release](https://github.com/Gui-Rondon/hyrule/blob/main/docs/releasing.md).

Os exemplos da documentação compilam. A marca de versão estável aparece na v1;
não depende de editar metadados. Attestations dos arquivos de release não atestam
automaticamente o zip do proxy Go; slsaProvenances no deps.dev é exclusivo de npm.

Consulte [CONTRIBUTING](https://github.com/Gui-Rondon/hyrule/blob/main/CONTRIBUTING.md).
Relatos de segurança usam o canal privado do GitHub, com resposta inicial em até
sete dias corridos. O mantenedor atual revisa as mudanças; aprovações independentes
obrigatórias dependem de novos membros. A proteção da branch será ativada depois
de os checks exigidos aparecerem e passarem; somente adicionar YAML não a habilita.
