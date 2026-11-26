# Benchmark: Claude Code com e sem o Codumi

Rodado em 13/11/2026 com `scripts/bench.sh`. Projeto: [maybe-finance/maybe](https://github.com/maybe-finance/maybe) (Rails, 693 arquivos). Modelo: Sonnet. Modo: só leitura (plan). Cada pergunta rodou numa sessão nova, uma vez sem MCP nenhum e uma vez com o Codumi (MCP, a dica e os ganchos que o app usa).

| Pergunta | Modo | Tokens processados | Tokens novos | Custo (preço de API) | Voltas | Tempo |
|---|---|---|---|---|---|---|
| 1. Onde se cria uma transação e em quais tabelas ela grava | sem | 228.434 | 33.583 | US$ 0,176 | 6 | 28,6 s |
| | com | 247.974 | 29.272 | US$ 0,154 | 7 | 22,4 s |
| 2. Rotas e controllers da importação de CSV e as tabelas | sem | 156.084 | 34.921 | US$ 0,167 | 5 | 19,7 s |
| | com | 253.497 | 28.885 | US$ 0,156 | 7 | 25,1 s |
| 3. Quem chama a sincronização da conta | sem | 173.895 | 25.362 | US$ 0,130 | 5 | 18,9 s |
| | com | 171.789 | 23.680 | US$ 0,120 | 6 | 14,6 s |

**Resumo.** Com o Codumi, os tokens novos caíram de 7% a 17% e o custo de 7% a 12% nas três perguntas. Os tokens totais (com a releitura do cache) subiram em duas, porque o agente deu uma volta a mais. As respostas tiveram a mesma qualidade.

**Limites deste teste.** São só 3 perguntas, num projeto Rails, rodadas uma vez cada, então a variação entre execuções pesa. Na pergunta 3 o agente caiu no grep, porque `@account.sync_later` chama um método de concern por variável de instância sem tipo conhecido, e o mapa não liga essa chamada.
